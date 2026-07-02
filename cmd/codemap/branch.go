/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

var (
	branchStatusCmd = &cobra.Command{
		Use:   "branch-status [path]",
		Short: "Show the git branch/commit state used to key per-branch index snapshots (read-only)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runBranchStatus,
	}
	branchSwitchCmd = &cobra.Command{
		Use:   "branch-switch",
		Short: "Switch the code index to a git branch (snapshots the old branch, restores/reindexes the new)",
		RunE:  runBranchSwitch,
	}
	branchSnapshotCmd = &cobra.Command{
		Use:   "branch-snapshot",
		Short: "Stash the current branch's code index into fcheap so it can be restored on switch-back",
		RunE:  runBranchSnapshot,
	}
)

// runBranchSwitch switches the code index to a branch (or installs the git hook
// that does it automatically on checkout).
func runBranchSwitch(cmd *cobra.Command, _ []string) error {
	root, _ := cmd.Flags().GetString("root")
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	if install, _ := cmd.Flags().GetBool("install-hook"); install {
		bin := "codemap"
		if exe, err := os.Executable(); err == nil { // pin the running binary so the hook works off-PATH
			bin = exe
		}
		path, err := app.InstallPostCheckoutHook(cmd.Context(), root, bin)
		if err != nil {
			return err
		}
		fmt.Printf("installed git post-checkout hook: %s\n", path)
		return nil
	}
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	svc := app.NewService(sess)
	to, _ := cmd.Flags().GetString("to")
	from, _ := cmd.Flags().GetString("from")
	if to == "" {
		st, _ := svc.BranchStatus(cmd.Context(), root)
		to = st.Branch
	}
	if to == "" {
		return fmt.Errorf("no target branch (detached HEAD or not a git repository) — pass --to")
	}
	if err := svc.BranchSwitch(cmd.Context(), root, from, to); err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(map[string]string{"switched_to": to})
	}
	fmt.Printf("code index switched to branch %q\n", to)
	return nil
}

// runBranchSnapshot stashes the current branch's index into fcheap.
func runBranchSnapshot(cmd *cobra.Command, _ []string) error {
	root, _ := cmd.Flags().GetString("root")
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	svc := app.NewService(sess)
	branch, _ := cmd.Flags().GetString("branch")
	if branch == "" {
		st, _ := svc.BranchStatus(cmd.Context(), root)
		branch = st.Branch
	}
	if branch == "" {
		return fmt.Errorf("no branch to snapshot (detached HEAD or not a git repository) — pass --branch")
	}
	if err := svc.BranchSnapshot(cmd.Context(), root, branch); err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(map[string]string{"snapshotted": branch})
	}
	fmt.Printf("snapshotted branch %q\n", branch)
	return nil
}

// runBranchStatus reports the read-only git state of the repo at the given path
// (or cwd) — the foundation for branch-aware index switching. No writes.
func runBranchStatus(cmd *cobra.Command, args []string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	if len(args) > 0 {
		dir = args[0]
	}
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	st, err := app.NewService(sess).BranchStatus(cmd.Context(), dir)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(st)
	}
	if !st.IsRepo {
		fmt.Printf("%s is not inside a git repository — per-branch index snapshots don't apply.\n", dir)
		return nil
	}
	branch := st.Branch
	if st.Detached {
		branch = "(detached HEAD)"
	}
	sha := st.SHA
	if len(sha) > 12 {
		sha = sha[:12]
	}
	fmt.Printf("Repo:   %s\n  hash:   %s\n  branch: %s\n  commit: %s\n", st.RepoRoot, st.RepoHash, branch, sha)
	if st.Key != "" {
		fmt.Printf("  index key: %s\n", st.Key)
	}
	return nil
}
