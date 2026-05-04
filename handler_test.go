package main

import (
	"context"
	"errors"
	"testing"

	"github.com/awslabs/soci-snapshotter/soci/store"
	"github.com/containerd/containerd/images"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	registryutils "github.com/CloudSnorkel/standalone-soci-indexer/utils/registry"
)

type fakeRegistry struct {
	headDescriptor ocispec.Descriptor
	headErr        error
	pullDescriptor ocispec.Descriptor
	validateErr    error
	buildCalls     int

	pullReferences []string
	pushes         []ocispec.Descriptor
	tags           []tagCall
	validateCalls  []string
}

type tagCall struct {
	desc ocispec.Descriptor
	tag  string
}

func (f *fakeRegistry) Pull(_ context.Context, _ string, _ *store.SociStore, imageReference string) (*ocispec.Descriptor, error) {
	f.pullReferences = append(f.pullReferences, imageReference)
	desc := f.pullDescriptor
	return &desc, nil
}

func (f *fakeRegistry) Push(_ context.Context, _ *store.SociStore, indexDesc ocispec.Descriptor, _ string) error {
	f.pushes = append(f.pushes, indexDesc)
	return nil
}

func (f *fakeRegistry) Tag(_ context.Context, indexDesc ocispec.Descriptor, _ string, tag string) error {
	f.tags = append(f.tags, tagCall{desc: indexDesc, tag: tag})
	return nil
}

func (f *fakeRegistry) HeadManifest(_ context.Context, _ string, _ string) (ocispec.Descriptor, error) {
	return f.headDescriptor, f.headErr
}

func (f *fakeRegistry) ValidateImageManifest(_ context.Context, _ string, digest string) error {
	f.validateCalls = append(f.validateCalls, digest)
	return f.validateErr
}

func installTestHooks(t *testing.T, registry *fakeRegistry, build func(context.Context, string, *store.SociStore, images.Image) (*ocispec.Descriptor, error)) {
	oldInitRegistry := initRegistry
	oldBuildIndexFn := buildIndexFn
	t.Cleanup(func() {
		initRegistry = oldInitRegistry
		buildIndexFn = oldBuildIndexFn
	})

	initRegistry = func(context.Context, string, string) (registryClient, error) {
		return registry, nil
	}
	buildIndexFn = build
}

func runIndexAndPushTest(t *testing.T, registry *fakeRegistry, build func(context.Context, string, *store.SociStore, images.Image) (*ocispec.Descriptor, error)) (string, error) {
	t.Helper()
	installTestHooks(t, registry, build)
	return indexAndPush(context.Background(), "example/repo", "latest", []string{"latest", "stable"}, "registry.example.com", "")
}

func TestIndexAndPush(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*testing.T) (*fakeRegistry, func(context.Context, string, *store.SociStore, images.Image) (*ocispec.Descriptor, error))
		assert func(*testing.T, *fakeRegistry, string, error)
	}{
		{
			name: "pushes aggregate index once for manifest list",
			setup: func(t *testing.T) (*fakeRegistry, func(context.Context, string, *store.SociStore, images.Image) (*ocispec.Descriptor, error)) {
				registry := &fakeRegistry{
					headDescriptor: ocispec.Descriptor{
						MediaType: registryutils.MediaTypeDockerManifestList,
						Digest:    digest.Digest("sha256:1111111111111111111111111111111111111111111111111111111111111111"),
					},
					pullDescriptor: ocispec.Descriptor{
						MediaType: registryutils.MediaTypeDockerManifestList,
						Digest:    digest.Digest("sha256:1111111111111111111111111111111111111111111111111111111111111111"),
					},
				}

				aggregateDesc := &ocispec.Descriptor{
					MediaType: ocispec.MediaTypeImageIndex,
					Digest:    digest.Digest("sha256:2222222222222222222222222222222222222222222222222222222222222222"),
					Size:      123,
				}

				build := func(_ context.Context, _ string, _ *store.SociStore, image images.Image) (*ocispec.Descriptor, error) {
					registry.buildCalls++
					if image.Name != "example/repo:latest" {
						t.Fatalf("unexpected image name: %s", image.Name)
					}
					if image.Target.Digest != registry.pullDescriptor.Digest {
						t.Fatalf("unexpected image digest: %s", image.Target.Digest)
					}
					return aggregateDesc, nil
				}

				return registry, build
			},
			assert: func(t *testing.T, registry *fakeRegistry, message string, err error) {
				if err != nil {
					t.Fatalf("indexAndPush returned error: %v", err)
				}
				if message != BuildAndPushSuccessMessage {
					t.Fatalf("unexpected message: %s", message)
				}
				if registry.buildCalls != 1 {
					t.Fatalf("expected buildIndex to be called once, got %d", registry.buildCalls)
				}
				if len(registry.pullReferences) != 1 || registry.pullReferences[0] != "latest" {
					t.Fatalf("unexpected pull references: %#v", registry.pullReferences)
				}
				if len(registry.pushes) != 1 || registry.pushes[0].Digest != digest.Digest("sha256:2222222222222222222222222222222222222222222222222222222222222222") {
					t.Fatalf("unexpected pushes: %#v", registry.pushes)
				}
				if len(registry.tags) != 2 {
					t.Fatalf("expected 2 tag calls, got %d", len(registry.tags))
				}
				if registry.tags[0].tag != "latest" || registry.tags[1].tag != "stable" {
					t.Fatalf("unexpected tag sequence: %#v", registry.tags)
				}
				for _, tagCall := range registry.tags {
					if tagCall.desc.Digest != digest.Digest("sha256:2222222222222222222222222222222222222222222222222222222222222222") {
						t.Fatalf("tagged wrong descriptor: %s", tagCall.desc.Digest)
					}
				}
				if len(registry.validateCalls) != 0 {
					t.Fatalf("did not expect image manifest validation calls for manifest list, got %#v", registry.validateCalls)
				}
			},
		},
		{
			name: "tags original image on empty index",
			setup: func(t *testing.T) (*fakeRegistry, func(context.Context, string, *store.SociStore, images.Image) (*ocispec.Descriptor, error)) {
				registry := &fakeRegistry{
					headDescriptor: ocispec.Descriptor{
						MediaType: registryutils.MediaTypeDockerManifest,
						Digest:    digest.Digest("sha256:3333333333333333333333333333333333333333333333333333333333333333"),
					},
					pullDescriptor: ocispec.Descriptor{
						MediaType: registryutils.MediaTypeDockerManifest,
						Digest:    digest.Digest("sha256:3333333333333333333333333333333333333333333333333333333333333333"),
					},
				}

				build := func(context.Context, string, *store.SociStore, images.Image) (*ocispec.Descriptor, error) {
					return nil, ErrEmptyIndex
				}

				return registry, build
			},
			assert: func(t *testing.T, registry *fakeRegistry, message string, err error) {
				if err != nil {
					t.Fatalf("indexAndPush returned error: %v", err)
				}
				if message != PushOnEmptyIndexMessage {
					t.Fatalf("unexpected message: %s", message)
				}
				if len(registry.pushes) != 0 {
					t.Fatalf("expected no pushes, got %#v", registry.pushes)
				}
				if len(registry.tags) != 1 || registry.tags[0].tag != "stable" {
					t.Fatalf("unexpected tag calls: %#v", registry.tags)
				}
				if registry.tags[0].desc.Digest != registry.pullDescriptor.Digest {
					t.Fatalf("expected original image descriptor to be tagged, got %s", registry.tags[0].desc.Digest)
				}
				if len(registry.validateCalls) != 1 || registry.validateCalls[0] != "sha256:3333333333333333333333333333333333333333333333333333333333333333" {
					t.Fatalf("unexpected validation calls: %#v", registry.validateCalls)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry, build := test.setup(t)
			message, err := runIndexAndPushTest(t, registry, build)
			test.assert(t, registry, message, err)
		})
	}
}

func TestResolveSourceImageDescriptor(t *testing.T) {
	validationErr := errors.New("validation failed")
	headErr := errors.New("head failed")

	tests := []struct {
		name                string
		registry            *fakeRegistry
		expectedDescriptor  ocispec.Descriptor
		expectedErr         error
		expectedErrMessage  string
		expectedValidations []string
	}{
		{
			name: "returns descriptor for manifest list without validation",
			registry: &fakeRegistry{
				headDescriptor: ocispec.Descriptor{
					MediaType: registryutils.MediaTypeDockerManifestList,
					Digest:    digest.Digest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
				},
			},
			expectedDescriptor: ocispec.Descriptor{
				MediaType: registryutils.MediaTypeDockerManifestList,
				Digest:    digest.Digest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			},
		},
		{
			name: "validates docker manifest",
			registry: &fakeRegistry{
				headDescriptor: ocispec.Descriptor{
					MediaType: registryutils.MediaTypeDockerManifest,
					Digest:    digest.Digest("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
				},
			},
			expectedDescriptor: ocispec.Descriptor{
				MediaType: registryutils.MediaTypeDockerManifest,
				Digest:    digest.Digest("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
			},
			expectedValidations: []string{"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		},
		{
			name: "returns head manifest error",
			registry: &fakeRegistry{
				headErr: headErr,
			},
			expectedErr: headErr,
		},
		{
			name: "returns validation error for image manifest",
			registry: &fakeRegistry{
				headDescriptor: ocispec.Descriptor{
					MediaType: registryutils.MediaTypeOCIManifest,
					Digest:    digest.Digest("sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
				},
				validateErr: validationErr,
			},
			expectedErr:         validationErr,
			expectedValidations: []string{"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},
		},
		{
			name: "rejects unexpected manifest type",
			registry: &fakeRegistry{
				headDescriptor: ocispec.Descriptor{
					MediaType: "application/example",
				},
			},
			expectedErrMessage:  "unexpected manifest media type: application/example",
			expectedValidations: []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			desc, err := resolveSourceImageDescriptor(context.Background(), test.registry, "example/repo", "latest")

			if test.expectedErr != nil {
				if !errors.Is(err, test.expectedErr) {
					t.Fatalf("expected error %v, got %v", test.expectedErr, err)
				}
			} else if test.expectedErrMessage != "" {
				if err == nil || err.Error() != test.expectedErrMessage {
					t.Fatalf("expected error %q, got %v", test.expectedErrMessage, err)
				}
			} else if err != nil {
				t.Fatalf("resolveSourceImageDescriptor returned error: %v", err)
			}

			if desc.MediaType != test.expectedDescriptor.MediaType || desc.Digest != test.expectedDescriptor.Digest {
				t.Fatalf("unexpected descriptor: %#v", desc)
			}

			if len(test.registry.validateCalls) != len(test.expectedValidations) {
				t.Fatalf("unexpected validation calls: %#v", test.registry.validateCalls)
			}
			for i, validation := range test.expectedValidations {
				if test.registry.validateCalls[i] != validation {
					t.Fatalf("unexpected validation calls: %#v", test.registry.validateCalls)
				}
			}
		})
	}
}
