package main

import (
	"testing"
)

func TestImageParsing(t *testing.T) {
	test := func(desc, expectedRepo, expectedTag, expectedRegistry string) {
		repo, tag, registry, err := parseImageDesc(desc)
		if err != nil {
			t.Error(err)
			return
		}
		if repo != expectedRepo || tag != expectedTag || registry != expectedRegistry {
			t.Errorf(
				"Image string: %s\nExpected:\n  repo=%s\n  tag=%s\n  registry=%s\nGot:\n  repo=%s\n  tag=%s\n  registry=%s",
				desc,
				expectedRepo, expectedTag, expectedRegistry,
				repo, tag, registry,
			)
		}
	}

	test("foo/bar", "foo/bar", "latest", "docker.io")
	test("foo/bar:version", "foo/bar", "version", "docker.io")
	test("foo/bar@sha256:9a161b6fc2f8ef74bb368f56edcac33a91b494d082da3693a600751a1a68b7d8", "foo/bar", "sha256:9a161b6fc2f8ef74bb368f56edcac33a91b494d082da3693a600751a1a68b7d8", "docker.io")
	test("public.ecr.aws/foo/bar", "foo/bar", "latest", "public.ecr.aws")
	test("public.ecr.aws/foo/bar:version", "foo/bar", "version", "public.ecr.aws")
	test("public.ecr.aws/foo/bar@sha256:9a161b6fc2f8ef74bb368f56edcac33a91b494d082da3693a600751a1a68b7d8", "foo/bar", "sha256:9a161b6fc2f8ef74bb368f56edcac33a91b494d082da3693a600751a1a68b7d8", "public.ecr.aws")
}
