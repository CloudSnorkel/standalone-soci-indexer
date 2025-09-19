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
	auth                   string
	newTags                []string
	allowPushOnEmptyIndex  bool
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

			if strings.Contains(tag, ":") && len(newTags) == 0 {
				log.Error(ctx, "Tag cannot be a digest without --new-tag", nil)
				os.Exit(1)
			}

			if tag == "" {
				log.Error(ctx, "Tag is required", nil)
				os.Exit(1)
			}

			if len(newTags) == 0 {
				newTags = append(newTags, tag)
			}

			log.Info(ctx, fmt.Sprintf("Indexing %s:%s and pushing with tags %s to %s", repo, tag, newTags, registry))

			_, err = indexAndPush(ctx, repo, tag, newTags, registry, auth, allowPushOnEmptyIndex)
			if err != nil {
				os.Exit(1)
			}
		},
	}

	rootCmd.Flags().StringVarP(&auth, "auth", "a", "", "Registry authentication token (usually USER:PASSWORD)")
	rootCmd.Flags().StringArrayVarP(&newTags, "new-tag", "t", nil, "Push indexed image with this tag")
	rootCmd.Flags().BoolVar(&allowPushOnEmptyIndex, "allow-push-on-empty-index", false, "Allow pushing even if the index is empty")

	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
