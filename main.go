package main

import (
	"context"
	"fmt"
	"github.com/CloudSnorkel/standalone-soci-indexer/utils/log"
	"github.com/spf13/cobra"
	"os"
	"strings"
)

var (
	version       = "dev"
	commit        = "none"
	date          = "unknown"
	builtBy       = "unknown"
	versionString = fmt.Sprintf("%s, commit %s, built at %s by %s", version, commit, date, builtBy)
)

var (
	auth string
)

func main() {
	var rootCmd = &cobra.Command{
		Use:     "soci-indexer [REGISTRY/]REPO[:TAG]",
		Short:   "Standalone SOCI indexer for a container image that both indexes and pushes the index",
		Version: versionString,
		Args:    cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var ctx = context.Background()

			var registry string
			var repo string
			var tag string
			var rest string

			splits := strings.SplitN(args[0], "/", 2)
			if len(splits) == 1 {
				registry = "docker.io"
				rest = splits[0]
			} else {
				registry = splits[0]
				rest = splits[1]
			}

			splits = strings.SplitN(rest, ":", 2)
			if len(splits) == 1 {
				repo = splits[0]
				tag = "latest"
			} else {
				repo = splits[0]
				tag = splits[1]
			}

			log.Info(ctx, fmt.Sprintf("Indexing and pushing %s:%s to %s", repo, tag, registry))

			_, err := indexAndPush(ctx, repo, tag, registry, auth)
			if err != nil {
				os.Exit(1)
			}
		},
	}

	rootCmd.Flags().StringVarP(&auth, "auth", "a", "", "Registry authentication token (usually USER:PASSWORD)")

	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
