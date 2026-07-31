package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

// runDump is the dev helper: fetch one API payload and print it to stdout,
// outside any pane. The repo comes from the cwd's git remote (or
// BBPR_WORKSPACE/BBPR_REPO in env).
//
//	bitbucket-pr dump prs [STATE]
//	bitbucket-pr dump pr <id>
//	bitbucket-pr dump diff <id>
//	bitbucket-pr dump diffstat <id>
//	bitbucket-pr dump comments <id>
func runDump(args []string) {
	if len(args) < 1 {
		errExit("usage: bitbucket-pr dump <prs [state]|pr <id>|diff <id>|parse <id>|diffstat <id>|comments <id>>")
	}
	cfg := loadConfig()
	email, token, ok := cfg.credentials()
	if !ok {
		errExit("credentials missing: set ATLASSIAN_USER_ID / ATLASSIAN_API_TOKEN")
	}
	ws, repo := os.Getenv(envWorkspace), os.Getenv(envRepo)
	if ws == "" || repo == "" {
		cwd, _ := os.Getwd()
		var err error
		if ws, repo, err = repoFromDir(cwd); err != nil {
			errExit(err)
		}
	}
	client := newBBClient(email, token, time.Duration(cfg.HTTPTimeoutSec)*time.Second)

	id := 0
	if len(args) > 1 {
		id, _ = strconv.Atoi(args[1])
	}
	needID := func() {
		if id <= 0 {
			errExit("usage: bitbucket-pr dump " + args[0] + " <pr-id>")
		}
	}

	switch args[0] {
	case "prs":
		state := cfg.DefaultState
		if len(args) > 1 {
			state = args[1]
		}
		prs, err := client.listPRs(ws, repo, state)
		exitOn(err)
		printJSON(prs)
	case "pr":
		needID()
		pr, err := client.getPR(ws, repo, id)
		exitOn(err)
		printJSON(pr)
	case "diff":
		needID()
		text, err := client.diff(ws, repo, id)
		exitOn(err)
		fmt.Print(text)
	case "parse":
		needID()
		text, err := client.diff(ws, repo, id)
		exitOn(err)
		for _, f := range ParseUnifiedDiff(text) {
			flags := ""
			if f.IsNew {
				flags += " new"
			}
			if f.IsDeleted {
				flags += " deleted"
			}
			if f.IsRename {
				flags += " rename"
			}
			if f.IsBinary {
				flags += " binary"
			}
			fmt.Printf("%s%s: %d hunks +%d -%d\n", f.DisplayPath(), flags, len(f.Hunks), f.Added(), f.Deleted())
		}
	case "diffstat":
		needID()
		st, err := client.diffStat(ws, repo, id)
		exitOn(err)
		printJSON(st)
	case "comments":
		needID()
		cs, err := client.comments(ws, repo, id)
		exitOn(err)
		printJSON(cs)
	default:
		errExit("unknown dump target:", args[0])
	}
}

func exitOn(err error) {
	if err != nil {
		errExit(err)
	}
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
