/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

var (
	annotateCmd = &cobra.Command{
		Use:   "annotate <symbol> | <from> <to>",
		Short: "Attach a note and/or external data (e.g. DB rows) to a symbol or a call path",
		Args:  cobra.RangeArgs(1, 2),
		RunE:  runAnnotate,
	}
	annotationsCmd = &cobra.Command{
		Use:   "annotations [symbol] | [from] [to]",
		Short: "List annotations (all, for a symbol, or for a from→to path); --rm <id> to remove",
		Args:  cobra.RangeArgs(0, 2),
		RunE:  runAnnotations,
	}
)

func runAnnotate(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	source, _ := cmd.Flags().GetString("source")
	note, _ := cmd.Flags().GetString("note")
	data, _ := cmd.Flags().GetString("data")
	if note == "" && data == "" {
		return fmt.Errorf("nothing to attach: pass --note and/or --data")
	}
	svc := app.NewService(sess)
	var (
		id     int64
		match  bool
		warn   string
		kind   = "node"
		target string
	)
	if len(args) == 1 {
		target = args[0]
		id, match, err = svc.AnnotateNode(cwd, target, source, note, data)
		if err == nil && !match {
			warn = fmt.Sprintf("no indexed symbol named %q — saved, but it won't surface in queries until one is (typo? not indexed yet?)", target)
		}
	} else {
		kind = "path"
		id, target, match, err = svc.AnnotatePath(cwd, args[0], args[1], source, note, data)
		if err == nil && !match {
			warn = fmt.Sprintf("path endpoints %q and %q aren't both indexed symbols — saved, but it won't surface until they are", args[0], args[1])
		}
	}
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		out := map[string]any{"id": id, "kind": kind, "target": target, "source": source, "matched": match}
		if warn != "" {
			out["note"] = warn
		}
		return printJSON(out)
	}
	label := target
	if kind == "path" {
		label = "path " + target
	}
	fmt.Printf("annotated %s  (#%d, source=%s)\n", label, id, source)
	if warn != "" {
		fmt.Println("⚠ " + warn)
	}
	return nil
}

func runAnnotations(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	svc := app.NewService(sess)

	if rm, _ := cmd.Flags().GetInt64("rm"); rm > 0 {
		ok, err := svc.RemoveAnnotation(cwd, rm)
		if err != nil {
			return err
		}
		if ok {
			fmt.Printf("removed annotation #%d\n", rm)
		} else {
			fmt.Printf("no annotation #%d\n", rm)
		}
		return nil
	}

	var rep *app.AnnotationsReport
	switch len(args) {
	case 0:
		rep, err = svc.AllAnnotations(cwd)
	case 1:
		rep, err = svc.NodeAnnotations(cwd, args[0])
	default:
		rep, err = svc.PathAnnotations(cwd, args[0], args[1])
	}
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Annotations) == 0 {
		fmt.Println("no annotations")
		return nil
	}
	dangling := make(map[int64]bool, len(rep.Dangling))
	for _, id := range rep.Dangling {
		dangling[id] = true
	}
	for _, a := range rep.Annotations {
		line := fmt.Sprintf("#%-4d %-5s %-8s %s", a.ID, a.Kind, a.Source, a.Target)
		if dangling[a.ID] {
			line += "  ⚠ no current symbol (renamed/removed — prune with --rm, or re-add)"
		}
		fmt.Println(line)
		if a.Note != "" {
			fmt.Printf("        note: %s\n", a.Note)
		}
		if a.Data != "" {
			fmt.Printf("        data: %s\n", truncStr(a.Data, 100))
		}
	}
	return nil
}
