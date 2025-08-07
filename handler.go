// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"

	"github.com/CloudSnorkel/standalone-soci-indexer/utils/log"
	registryutils "github.com/CloudSnorkel/standalone-soci-indexer/utils/registry"
	"github.com/containerd/containerd/images"
	"oras.land/oras-go/v2/content/oci"

	"github.com/awslabs/soci-snapshotter/soci"
	"github.com/awslabs/soci-snapshotter/soci/store"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// TODO: Remove this once the SOCI library exports this error.
var (
	ErrEmptyIndex = errors.New("no ztocs created, all layers either skipped or produced errors")
)

const (
	BuildFailedMessage          = "SOCI index build error"
	PushFailedMessage           = "SOCI index push error"
	SkipPushOnEmptyIndexMessage = "Skipping pushing SOCI index as it does not contain any zTOCs"
	BuildAndPushSuccessMessage  = "Successfully built and pushed SOCI index"

	artifactsStoreName = "store"
	artifactsDbName    = "artifacts.db"
)

func indexAndPush(ctx context.Context, repo string, tag string, registryUrl string, authToken string) (string, error) {
	ctx = context.WithValue(ctx, "RegistryURL", registryUrl)

	registry, err := registryutils.Init(ctx, registryUrl, authToken)
	if err != nil {
		return logAndReturnError(ctx, "Remote registry initialization error", err)
	}

	digests, err := registry.GetImageDigests(ctx, repo, tag)
	if err != nil {
		log.Warn(ctx, fmt.Sprintf("Image manifest validation error: %v", err))
		// Returning a non error to skip retries
		return "Exited early due to manifest validation error", nil
	}

	// Directory in lambda storage to store images and SOCI artifacts
	dataDir, err := createTempDir(ctx)
	if err != nil {
		return logAndReturnError(ctx, "Directory create error", err)
	}
	defer cleanUp(ctx, dataDir)

	sociStore, err := initSociStore(ctx, dataDir)
	if err != nil {
		return logAndReturnError(ctx, "OCI storage initialization error", err)
	}

	for _, digest := range digests {
		ctx := context.WithValue(ctx, "ImageDigest", digest)
		desc, err := registry.Pull(ctx, repo, sociStore, digest)
		if err != nil {
			return logAndReturnError(ctx, "Image pull error", err)
		}

		image := images.Image{
			Name:   repo + "@" + digest,
			Target: *desc,
		}

		indexDescriptor, err := buildIndex(ctx, dataDir, sociStore, image)
		if err != nil {
			if err.Error() == ErrEmptyIndex.Error() {
				log.Warn(ctx, SkipPushOnEmptyIndexMessage)
				return SkipPushOnEmptyIndexMessage, nil
			}
			return logAndReturnError(ctx, BuildFailedMessage, err)
		}
		ctx = context.WithValue(ctx, "SOCIIndexDigest", indexDescriptor.Digest.String())

		err = registry.Push(ctx, sociStore, *indexDescriptor, repo, tag)
		if err != nil {
			return logAndReturnError(ctx, PushFailedMessage, err)
		}

		log.Info(ctx, BuildAndPushSuccessMessage)
	}
	return BuildAndPushSuccessMessage, nil
}

// Create a temp directory in /tmp or $TMPDIR
// The directory is prefixed by the Lambda's request id
func createTempDir(ctx context.Context) (string, error) {
	log.Info(ctx, "Creating a directory to store images and SOCI artifacts")
	tempDir, err := os.MkdirTemp("", "soci") // The temp dir name is prefixed by the request id
	return tempDir, err
}

// Clean up the data written by the Lambda
func cleanUp(ctx context.Context, dataDir string) {
	log.Info(ctx, fmt.Sprintf("Removing all files in %s", dataDir))
	if err := os.RemoveAll(dataDir); err != nil {
		log.Error(ctx, "Clean up error", err)
	}
}

// Init containerd store
func initContainerdStore(dataDir string) (content.Store, error) {
	containerdStore, err := local.NewStore(path.Join(dataDir, artifactsStoreName))
	return containerdStore, err
}

// Init SOCI artifact store
func initSociStore(ctx context.Context, dataDir string) (*store.SociStore, error) {
	// Note: We are wrapping an *oci.Store in a store.SociStore because soci.WriteSociIndex
	// expects a store.Store, an interface that extends the oci.Store to provide support
	// for garbage collection.
	ociStore, err := oci.NewWithContext(ctx, path.Join(dataDir, artifactsStoreName))
	return &store.SociStore{ociStore}, err
}

// Init a new instance of SOCI artifacts DB
func initSociArtifactsDb(dataDir string) (*soci.ArtifactsDb, error) {
	artifactsDbPath := path.Join(dataDir, artifactsDbName)
	artifactsDb, err := soci.NewDB(artifactsDbPath)
	if err != nil {
		return nil, err
	}
	return artifactsDb, nil
}

// Build soci index for an image and returns its ocispec.Descriptor
func buildIndex(ctx context.Context, dataDir string, sociStore *store.SociStore, image images.Image) (*ocispec.Descriptor, error) {
	log.Info(ctx, "Building SOCI index")

	artifactsDb, err := initSociArtifactsDb(dataDir)
	if err != nil {
		return nil, err
	}

	containerdStore, err := initContainerdStore(dataDir)
	if err != nil {
		return nil, err
	}

	builder, err := soci.NewIndexBuilder(containerdStore, sociStore, soci.WithArtifactsDb(artifactsDb), soci.WithBuildToolIdentifier("github.com/CloudSnorkel/standalone-soci-indexer"))
	if err != nil {
		return nil, err
	}

	index, err := builder.Convert(ctx, image)
	return index, err
}

// Log and return error
func logAndReturnError(ctx context.Context, msg string, err error) (string, error) {
	log.Error(ctx, msg, err)
	return msg, err
}
