package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/CloudSnorkel/standalone-soci-indexer/utils/log"
	parser "github.com/novln/docker-parser"
	"github.com/spf13/cobra"
)

var (
	version       = "dev"
	commit        = "none"
	date          = "unknown"
	builtBy       = "unknown"
	versionString = fmt.Sprintf("%s, commit %s, built at %s by %s", version, commit, date, builtBy)
)

var (
	auth   string
	newTag string
)

func parseImageDesc(desc string) (repo, tag, registry string, err error) {
	ref, err := parser.Parse(desc)
	if err != nil {
		return
	}

	repo = ref.ShortName()
	tag = ref.Tag()
	registry = ref.Registry()

	return
}

func main() {
	var rootCmd = &cobra.Command{
		Use:     "soci-indexer [REGISTRY/]REPO[:TAG]",
		Short:   "Standalone SOCI indexer for a container image that both indexes and pushes the index",
		Version: versionString,
		Args:    cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var ctx = context.Background()

			repo, tag, registry, err := parseImageDesc(args[0])
			if err != nil {
				log.Error(ctx, "Error parsing image reference: %s", err)
				os.Exit(1)
			}

			if strings.Contains(tag, ":") {
				log.Error(ctx, "Tag cannot be a digest", nil)
				os.Exit(1)
			}

			if tag == "" {
				log.Error(ctx, "Tag is required", nil)
				os.Exit(1)
			}

			if newTag == "" {
				newTag = tag
			}

			log.Info(ctx, fmt.Sprintf("Indexing %s:%s and pushing with tag %s to %s", repo, tag, newTag, registry))

			_, err = indexAndPush(ctx, repo, tag, newTag, registry, auth)
			if err != nil {
				os.Exit(1)
			}
		},
	}

	rootCmd.Flags().StringVarP(&auth, "auth", "a", "", "Registry authentication token (usually USER:PASSWORD)")
	rootCmd.Flags().StringVarP(&newTag, "new-tag", "t", "", "Push indexed image with this tag")

	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
