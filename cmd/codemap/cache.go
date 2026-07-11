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

func init() {
	cacheListCmd.Flags().Bool("rebuild", false, "reconstruct the list from fcheap (use if the local pointer file is lost)")
	cacheDropCmd.Flags().String("tree", "", "tree hash of the cache entry to drop")
	cacheDropCmd.Flags().Bool("all", false, "drop ALL cached indexes for this repo")
	cacheCmd.AddCommand(cacheSaveCmd, cacheRestoreCmd, cacheListCmd, cacheDropCmd)
	rootCmd.AddCommand(cacheCmd)
}
