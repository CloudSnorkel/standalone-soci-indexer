// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/awslabs/soci-snapshotter/soci/store"

	"github.com/CloudSnorkel/standalone-soci-indexer/utils/log"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	MediaTypeDockerManifestList = "application/vnd.docker.distribution.manifest.list.v2+json"
	MediaTypeDockerManifest     = "application/vnd.docker.distribution.manifest.v2+json"
	MediaTypeOCIManifest        = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeOCIIndexManifest   = "application/vnd.oci.image.index.v1+json"

	MediaTypeDockerImageConfig = "application/vnd.docker.container.image.v1+json"
	MediaTypeOCIImageConfig    = "application/vnd.oci.image.config.v1+json"
)

// List of config's media type for images
var ImageConfigMediaTypes = []string{MediaTypeDockerImageConfig, MediaTypeOCIImageConfig}

type Registry struct {
	registry *remote.Registry
}

var RegistryNotSupportingOciArtifacts = errors.New("Registry does not support OCI artifacts")
var ImageAlreadyIndexed = errors.New("Image already indexed")

type Manifest struct {
	ocispec.Manifest

	Manifests []ocispec.Descriptor `json:"manifests"`
}

// Initialize a remote registry
func Init(ctx context.Context, registryUrl string, authToken string) (*Registry, error) {
	log.Info(ctx, "Initializing registry client")
	registry, err := remote.NewRegistry(registryUrl)
	if err != nil {
		return nil, err
	}
	if authToken != "" {
		registry.RepositoryOptions.Client = &auth.Client{
			Header: http.Header{
				"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte(authToken))},
				"User-Agent":    {"Standalone SOCI Index Builder (oras-go)"},
			},
		}
		log.Info(ctx, "Using auth token")
	} else if isEcrRegistry(registryUrl) {
		err := authorizeEcr(ctx, registry)
		if err != nil {
			return nil, err
		}
	}
	return &Registry{registry}, nil
}

// Pull an image from the remote registry to a local OCI Store
// imageReference can be either a digest or a tag
func (registry *Registry) Pull(ctx context.Context, repositoryName string, sociStore *store.SociStore, imageReference string) (*ocispec.Descriptor, error) {
	log.Info(ctx, "Pulling image")
	repo, err := registry.registry.Repository(ctx, repositoryName)
	if err != nil {
		return nil, err
	}

	imageDescriptor, err := oras.Copy(ctx, repo, imageReference, sociStore, imageReference, oras.DefaultCopyOptions)
	if err != nil {
		return nil, err
	}

	return &imageDescriptor, nil
}

// Push a OCI artifact to remote registry
// descriptor: ocispec Descriptor of the artifact
// ociStore: the local OCI store
func (registry *Registry) Push(ctx context.Context, sociStore *store.SociStore, indexDesc ocispec.Descriptor, repositoryName string) error {
	log.Info(ctx, "Pushing artifact")

	repo, err := registry.registry.Repository(ctx, repositoryName)
	if err != nil {
		return err
	}

	err = oras.CopyGraph(ctx, sociStore, repo, indexDesc, oras.DefaultCopyGraphOptions)
	if err != nil {
		// TODO: There might be a better way to check if a registry supporting OCI or not
		if strings.Contains(err.Error(), "Response status code 405: unsupported: Invalid parameter at 'ImageManifest' failed to satisfy constraint: 'Invalid JSON syntax'") {
			log.Warn(ctx, fmt.Sprintf("Error when pushing: %v", err))
			return RegistryNotSupportingOciArtifacts
		}
		return err
	}

	return nil
}

func (registry *Registry) Tag(ctx context.Context, indexDesc ocispec.Descriptor, repositoryName, tag string) error {
	repo, err := registry.registry.Repository(ctx, repositoryName)
	if err != nil {
		return err
	}

	log.Info(ctx, fmt.Sprintf("Tagging index with %s", tag))
	err = repo.Tag(ctx, indexDesc, tag)
	if err != nil {
		return fmt.Errorf("failed to tag artifact: %w", err)
	}

	return nil
}

// Call registry's headManifest and return the manifest's descriptor
func (registry *Registry) HeadManifest(ctx context.Context, repositoryName string, reference string) (ocispec.Descriptor, error) {
	repo, err := registry.registry.Repository(ctx, repositoryName)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	descriptor, err := repo.Resolve(ctx, reference)
	if err != nil {
		return descriptor, err
	}

	return descriptor, nil
}

// Call registry's getManifest and return the image's manifest
// The image reference must be a digest because that's what oras-go FetchReference takes
func (registry *Registry) GetManifest(ctx context.Context, repositoryName string, digest string) (Manifest, error) {
	repo, err := registry.registry.Repository(ctx, repositoryName)
	var manifest Manifest
	if err != nil {
		return manifest, err
	}

	_, rc, err := repo.FetchReference(ctx, digest)
	if err != nil {
		return manifest, err
	}

	bytes, err := io.ReadAll(rc)
	if err != nil {
		return manifest, err
	}

	err = json.Unmarshal(bytes, &manifest)
	if err != nil {
		return manifest, err
	}

	return manifest, nil
}

// Validate if a digest is a valid image manifest
func (registry *Registry) ValidateImageManifest(ctx context.Context, repositoryName string, digest string) error {
	manifest, err := registry.GetManifest(ctx, repositoryName, digest)
	if err != nil {
		return err
	}

	if manifest.Config.MediaType == "" {
		return fmt.Errorf("Empty config media type.")
	}

	for _, configMediaType := range ImageConfigMediaTypes {
		if manifest.Config.MediaType == configMediaType {
			return nil
		}
	}

	return fmt.Errorf("Unexpected config media type: %s, expected one of: %v.", manifest.Config.MediaType, ImageConfigMediaTypes)
}

// GetImageDigests inspects an image reference and returns all valid digets that need to be indexed.
// For multi-arch images (docker manifest), that includes all digests mentioned by the manifest.
// For normal images, it's just the image digest itself.
func (registry *Registry) GetImageDigests(ctx context.Context, repositoryName string, digest string) (digests []string, err error) {
	manifest, err := registry.GetManifest(ctx, repositoryName, digest)
	if err != nil {
		return
	}

	if manifest.MediaType == MediaTypeOCIIndexManifest {
		err = ImageAlreadyIndexed
		return
	}

	if manifest.MediaType == MediaTypeDockerManifestList {
		// multi-arch iamge
		for _, internalManifest := range manifest.Manifests {
			if internalManifest.MediaType == MediaTypeDockerManifest {
				internalDigest := fmt.Sprintf("%s:%s", internalManifest.Digest.Algorithm().String(), internalManifest.Digest.Encoded())
				if registry.ValidateImageManifest(ctx, repositoryName, internalDigest) == nil {
					digests = append(digests, internalDigest)
				}
			}
		}

		if len(digests) == 0 {
			err = fmt.Errorf("Manifest contains no valid images.")
		}

		return
	}

	if manifest.Config.MediaType == "" {
		err = fmt.Errorf("Empty config media type.")
		return
	}

	for _, configMediaType := range ImageConfigMediaTypes {
		if manifest.Config.MediaType == configMediaType {
			digests = append(digests, digest)
			return
		}
	}

	err = fmt.Errorf("Unexpected config media type: %s, expected one of: %v.", manifest.Config.MediaType, ImageConfigMediaTypes)
	return
}

// Check if a registry is an ECR registry
func isEcrRegistry(registryUrl string) bool {
	ecrRegistryUrlRegex := "\\d{12}\\.dkr\\.ecr\\.\\S+\\.amazonaws\\.com"
	match, err := regexp.MatchString(ecrRegistryUrlRegex, registryUrl)
	if err != nil {
		panic(err)
	}
	return match
}

// Authorize ECR registry
func authorizeEcr(ctx context.Context, ecrRegistry *remote.Registry) error {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	var ecrClient *ecr.Client
	ecrEndpoint := os.Getenv("ECR_ENDPOINT") // set this env var for custom, i.e. non default, aws ecr endpoint
	if ecrEndpoint != "" {
		ecrClient = ecr.NewFromConfig(cfg, func(o *ecr.Options) {
			o.BaseEndpoint = aws.String(ecrEndpoint)
		})
	} else {
		ecrClient = ecr.NewFromConfig(cfg)
	}

	getAuthorizationTokenResponse, err := ecrClient.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return err
	}

	if len(getAuthorizationTokenResponse.AuthorizationData) == 0 {
		return errors.New("Couldn't authorize with ECR: empty authorization data returned")
	}

	ecrAuthorizationToken := getAuthorizationTokenResponse.AuthorizationData[0].AuthorizationToken
	if len(*ecrAuthorizationToken) == 0 {
		return errors.New("Couldn't authorize with ECR: empty authorization token returned")
	}

	ecrRegistry.RepositoryOptions.Client = &auth.Client{
		Header: http.Header{
			"Authorization": {"Basic " + *ecrAuthorizationToken},
			"User-Agent":    {"Standalone SOCI Index Builder (oras-go)"},
		},
	}
	return nil
}
