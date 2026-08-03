package main

import "testing"

func TestUIContextFromEnvIncludesRepoDir(t *testing.T) {
	t.Setenv(envWorkspace, "workspace")
	t.Setenv(envRepo, "repo")
	t.Setenv(envRepoDir, "/checkout/repo")
	t.Setenv(envPRID, "32")
	ctx, err := uiContextFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Workspace != "workspace" || ctx.Repo != "repo" || ctx.RepoDir != "/checkout/repo" || ctx.PRID != 32 {
		t.Fatalf("context = %#v", ctx)
	}
}

func TestUIContextFromEnvAllowsMissingRepoDir(t *testing.T) {
	t.Setenv(envWorkspace, "workspace")
	t.Setenv(envRepo, "repo")
	t.Setenv(envRepoDir, "")
	t.Setenv(envPRID, "")
	ctx, err := uiContextFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if ctx.RepoDir != "" || ctx.PRID != 0 {
		t.Fatalf("context = %#v", ctx)
	}
}

func TestUIContextFromEnvPickerModeAllowsMissingRepo(t *testing.T) {
	t.Setenv(envWorkspace, "workspace")
	t.Setenv(envRepo, "")
	t.Setenv(envRepoDir, "")
	t.Setenv(envPRID, "")
	t.Setenv(envMode, "picker")
	ctx, err := uiContextFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Mode != "picker" || ctx.Repo != "" {
		t.Fatalf("context = %#v", ctx)
	}
}

func TestUIContextFromEnvRejectsMissingRepoWithoutPickerMode(t *testing.T) {
	t.Setenv(envWorkspace, "workspace")
	t.Setenv(envRepo, "")
	t.Setenv(envMode, "")
	if _, err := uiContextFromEnv(); err == nil {
		t.Fatal("missing repo without picker mode must be an error")
	}
}
