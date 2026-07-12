/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Manage fcheap-cached index snapshots",
	Long: `Save, restore, list, and drop codemap index snapshots stashed in fcheap's
content-addressed vault. A cache entry is keyed by a tree hash — a hash of all
indexed (file_path, file_content_hash) pairs — so two working trees with identical
code share one cache entry (fcheap dedups the content). This lets codemap skip a
full reindex when the working tree matches a previously-saved index.`,
}

var cacheSaveCmd = &cobra.Command{
	Use:   "save",
	Short: "Stash the current index into the fcheap cache",
	RunE: func(cmd *cobra.Command, args []string) error {
		sess, err := openSession(cmd)
		if err != nil {
			return err
		}
		defer func() { _ = sess.Close() }()
		svc := app.NewService(sess)
		stashID, treeHash, err := svc.CacheSave(context.Background(), targetDir(cmd))
		if err != nil {
			return err
		}
		if stashID == "" {
			if jsonOut(cmd) {
				return printJSON(map[string]string{"stash_id": "", "note": "nothing to cache (project not indexed or not a git repo)"})
			}
			fmt.Println("nothing to cache (project not indexed or not a git repo)")
			return nil
		}
		if jsonOut(cmd) {
			return printJSON(map[string]string{"stash_id": stashID, "tree_hash": treeHash})
		}
		fmt.Printf("cached index saved: stash %s (tree %s)\n", stashID, shortHash(treeHash, 12))
		return nil
	},
}

var cacheRestoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore a cached index matching the current working tree",
	RunE: func(cmd *cobra.Command, args []string) error {
		sess, err := openSession(cmd)
		if err != nil {
			return err
		}
		defer func() { _ = sess.Close() }()
		svc := app.NewService(sess)
		restored, stashID, err := svc.CacheRestore(context.Background(), targetDir(cmd))
		if err != nil {
			return err
		}
		if jsonOut(cmd) {
			return printJSON(map[string]any{"restored": restored, "stash_id": stashID})
		}
		if restored {
			fmt.Printf("index restored from cache: stash %s\n", stashID)
		} else {
			fmt.Println("no matching cache entry found (run 'codemap index' to build the index)")
		}
		return nil
	},
}

var cacheListCmd = &cobra.Command{
	Use:   "list",
	Short: "List cached index snapshots for this repo",
	RunE: func(cmd *cobra.Command, args []string) error {
		sess, err := openSession(cmd)
		if err != nil {
			return err
		}
		defer func() { _ = sess.Close() }()
		svc := app.NewService(sess)
		rebuild, _ := cmd.Flags().GetBool("rebuild")
		rep, err := svc.CacheList(context.Background(), targetDir(cmd), rebuild)
		if err != nil {
			return err
		}
		if jsonOut(cmd) {
			return printJSON(rep)
		}
		if len(rep.Entries) == 0 {
			fmt.Println("no cached indexes for this repo")
			return nil
		}
		fmt.Printf("%d cached index(es) for repo %s:\n", len(rep.Entries), rep.RepoHash)
		for _, e := range rep.Entries {
			age := ""
			if e.SavedAt != "" {
				if t, terr := time.Parse(time.RFC3339, e.SavedAt); terr == nil {
					age = fmt.Sprintf(" (%s ago)", time.Since(t).Round(time.Second))
				}
			}
			fmt.Printf("  stash %s  tree %s  %d nodes  %d vectors  %s%s\n",
				e.StashID, shortHash(e.TreeHash, 12), e.NodeCount, e.VectorCount, e.EmbeddingProfile, age)
		}
		return nil
	},
}

var cacheDropCmd = &cobra.Command{
	Use:   "drop",
	Short: "Drop a cached index from fcheap",
	RunE: func(cmd *cobra.Command, args []string) error {
		sess, err := openSession(cmd)
		if err != nil {
			return err
		}
		defer func() { _ = sess.Close() }()
		svc := app.NewService(sess)
		treeHash, _ := cmd.Flags().GetString("tree")
		all, _ := cmd.Flags().GetBool("all")
		if treeHash == "" && !all {
			return fmt.Errorf("specify --tree <hash> or --all")
		}
		dropped, err := svc.CacheDrop(context.Background(), targetDir(cmd), treeHash, all)
		if err != nil {
			return err
		}
		if jsonOut(cmd) {
			return printJSON(map[string]int{"dropped": dropped})
		}
		if dropped == 0 {
			fmt.Println("no matching cache entries to drop")
		} else {
			fmt.Printf("dropped %d cache entr(y/ies) from fcheap\n", dropped)
		}
		return nil
	},
}

var cacheExportCmd = &cobra.Command{
	Use:   "export <file.tar.gz>",
	Short: "Export the current index to a portable, team/CI-shareable tarball",
	Long: `Export the project's current index (graph + vectors, when present) to a
self-contained tar.gz — the same snapshot codemap's fcheap-backed cache uses,
but portable: no shared fcheap store, no same-machine requirement. Hand the
file to another runner (or teammate) and 'codemap cache import' restores it
with no re-indexing.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sess, err := openSession(cmd)
		if err != nil {
			return err
		}
		defer func() { _ = sess.Close() }()
		svc := app.NewService(sess)
		rep, err := svc.CacheExport(context.Background(), targetDir(cmd), args[0])
		if err != nil {
			return err
		}
		if jsonOut(cmd) {
			return printJSON(rep)
		}
		fmt.Printf("exported index to %s (tree %s, %d nodes, %d edges, %d vectors)\n",
			rep.Path, shortHash(rep.TreeHash, 12), rep.Nodes, rep.Edges, rep.Vectors)
		return nil
	},
}

var cacheImportCmd = &cobra.Command{
	Use:   "import <file.tar.gz>",
	Short: "Import a portable index tarball produced by 'cache export'",
	Long: `Import a tarball produced by 'codemap cache export' into the project at the
current (or --path) directory, registering it first if it isn't indexed yet —
the common CI case: a fresh checkout with no prior 'codemap init'/'index'.

Refuses (exit 1) when: the tarball's wrapper schema is unsupported, its
embedding profile disagrees with the current provider/model (never mixes
embedding spaces, force or not), or its tree hash doesn't match the current
working tree (pass --force to import anyway — e.g. seeding a PR branch's
index from its base branch ahead of an incremental catch-up reindex).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sess, err := openSession(cmd)
		if err != nil {
			return err
		}
		defer func() { _ = sess.Close() }()
		svc := app.NewService(sess)
		force, _ := cmd.Flags().GetBool("force")
		rep, err := svc.CacheImport(context.Background(), targetDir(cmd), args[0], force)
		if err != nil {
			return err
		}
		if jsonOut(cmd) {
			return printJSON(rep)
		}
		if rep.Warning != "" {
			fmt.Println("warning:", rep.Warning)
		}
		fmt.Printf("imported index from %s (project %s, %d nodes, %d edges, %d vectors)\n",
			rep.Path, rep.Project, rep.Nodes, rep.Edges, rep.Vectors)
		return nil
	},
}

func init() {
	cacheListCmd.Flags().Bool("rebuild", false, "reconstruct the list from fcheap (use if the local pointer file is lost)")
	cacheDropCmd.Flags().String("tree", "", "tree hash of the cache entry to drop")
	cacheDropCmd.Flags().Bool("all", false, "drop ALL cached indexes for this repo")
	cacheImportCmd.Flags().Bool("force", false, "import even if the archive's tree hash doesn't match the current working tree")
	cacheCmd.AddCommand(cacheSaveCmd, cacheRestoreCmd, cacheListCmd, cacheDropCmd, cacheExportCmd, cacheImportCmd)
	rootCmd.AddCommand(cacheCmd)
}
