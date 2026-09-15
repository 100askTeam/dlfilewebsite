package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"dladmin-go/internal/release"
	"dladmin-go/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dlctl:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		return usageError()
	}
	switch args[0] {
	case "verify":
		if len(args) != 2 {
			return usageError()
		}
		keyText, err := readPublicKey()
		if err != nil {
			return err
		}
		key, err := release.DecodePublicKey(keyText)
		if err != nil {
			return err
		}
		manifest, err := release.LoadAndVerify(args[1], key)
		if err != nil {
			return err
		}
		return printJSON(manifest)
	case "import":
		flags := flag.NewFlagSet("import", flag.ContinueOnError)
		publish := flags.Bool("publish", false, "publish immediately after validation")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 1 {
			return usageError()
		}
		service, storage, actorID, err := openService()
		if err != nil {
			return err
		}
		defer storage.Close()
		record, err := service.ImportIncoming(context.Background(), flags.Arg(0), actorID)
		if err != nil {
			_ = storage.AppendAudit(context.Background(), &actorID, "dlctl", "import", "release", flags.Arg(0), "local-cli", "dlctl", "failure", detail(err))
			return err
		}
		_ = storage.AppendAudit(context.Background(), &actorID, "dlctl", "stage", "release", strconv.FormatInt(record.ID, 10), "local-cli", "dlctl", "success", detail(nil))
		if *publish {
			record, err = service.Publish(context.Background(), record.ID)
			if err != nil {
				_ = storage.AppendAudit(context.Background(), &actorID, "dlctl", "publish", "release", strconv.FormatInt(record.ID, 10), "local-cli", "dlctl", "failure", detail(err))
				return err
			}
			_ = storage.AppendAudit(context.Background(), &actorID, "dlctl", "publish", "release", strconv.FormatInt(record.ID, 10), "local-cli", "dlctl", "success", detail(nil))
		}
		return printJSON(record)
	case "publish":
		if len(args) != 2 {
			return usageError()
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || id < 1 {
			return errors.New("release id must be a positive integer")
		}
		service, storage, actorID, err := openService()
		if err != nil {
			return err
		}
		defer storage.Close()
		record, err := service.Publish(context.Background(), id)
		if err != nil {
			_ = storage.AppendAudit(context.Background(), &actorID, "dlctl", "publish", "release", args[1], "local-cli", "dlctl", "failure", detail(err))
			return err
		}
		_ = storage.AppendAudit(context.Background(), &actorID, "dlctl", "publish", "release", args[1], "local-cli", "dlctl", "success", detail(nil))
		return printJSON(record)
	case "restore-published":
		if len(args) != 2 {
			return usageError()
		}
		service, storage, actorID, err := openService()
		if err != nil {
			return err
		}
		defer storage.Close()
		record, err := service.RestorePublished(context.Background(), args[1])
		if err != nil {
			_ = storage.AppendAudit(context.Background(), &actorID, "dlctl", "restore", "release", args[1], "local-cli", "dlctl", "failure", detail(err))
			return err
		}
		_ = storage.AppendAudit(context.Background(), &actorID, "dlctl", "restore", "release", strconv.FormatInt(record.ID, 10), "local-cli", "dlctl", "success", detail(nil))
		return printJSON(record)
	case "list":
		storage, err := openStore()
		if err != nil {
			return err
		}
		defer storage.Close()
		records, err := storage.ListReleases(context.Background(), 200)
		if err != nil {
			return err
		}
		return printJSON(records)
	case "migrate-tools-layout":
		flags := flag.NewFlagSet("migrate-tools-layout", flag.ContinueOnError)
		dryRun := flags.Bool("dry-run", false, "verify and print the migration plan without changing files or metadata")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return usageError()
		}
		service, storage, actorID, err := openService()
		if err != nil {
			return err
		}
		defer storage.Close()
		migrations, err := service.MigrateLegacyLayout(context.Background(), *dryRun)
		if err != nil {
			_ = storage.AppendAudit(context.Background(), &actorID, "dlctl", "migrate", "release_layout", "Tools", "local-cli", "dlctl", "failure", detail(err))
			return err
		}
		action := "migrate"
		resultKey := "migrated"
		if *dryRun {
			action = "migrate_check"
			resultKey = "planned"
		}
		_ = storage.AppendAudit(context.Background(), &actorID, "dlctl", action, "release_layout", "Tools", "local-cli", "dlctl", "success", fmt.Sprintf(`{"count":%d}`, len(migrations)))
		return printJSON(map[string]any{resultKey: migrations, "count": len(migrations), "dry_run": *dryRun})
	default:
		return usageError()
	}
}

func openService() (*release.Service, *store.Store, int64, error) {
	stateDir := strings.TrimSpace(os.Getenv("DL_STATE_DIR"))
	publicDir := strings.TrimSpace(os.Getenv("DL_PUBLIC_DIR"))
	if stateDir == "" || publicDir == "" {
		return nil, nil, 0, errors.New("DL_STATE_DIR and DL_PUBLIC_DIR are required")
	}
	storage, err := store.Open(filepath.Join(stateDir, "dladmin.db"))
	if err != nil {
		return nil, nil, 0, err
	}
	actor := strings.TrimSpace(os.Getenv("DL_RELEASE_ACTOR"))
	if actor == "" {
		actor = strings.TrimSpace(os.Getenv("DL_ADMIN_USERNAME"))
	}
	if actor == "" {
		actor = "admin"
	}
	user, err := storage.UserByUsername(context.Background(), actor)
	if err != nil {
		_ = storage.Close()
		return nil, nil, 0, fmt.Errorf("release actor %q does not exist: %w", actor, err)
	}
	if user.Disabled || (user.Role != "admin" && user.Role != "release_manager") {
		_ = storage.Close()
		return nil, nil, 0, fmt.Errorf("release actor %q cannot publish", actor)
	}
	keyText, err := readPublicKey()
	if err != nil {
		_ = storage.Close()
		return nil, nil, 0, err
	}
	service, err := release.NewService(storage, stateDir, publicDir, keyText)
	if err != nil {
		_ = storage.Close()
		return nil, nil, 0, err
	}
	return service, storage, user.ID, nil
}

func openStore() (*store.Store, error) {
	stateDir := strings.TrimSpace(os.Getenv("DL_STATE_DIR"))
	if stateDir == "" {
		return nil, errors.New("DL_STATE_DIR is required")
	}
	return store.Open(filepath.Join(stateDir, "dladmin.db"))
}

func readPublicKey() (string, error) {
	if file := strings.TrimSpace(os.Getenv("DL_RELEASE_PUBLIC_KEY_FILE")); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read DL_RELEASE_PUBLIC_KEY_FILE: %w", err)
		}
		return string(data), nil
	}
	return strings.TrimSpace(os.Getenv("DL_RELEASE_PUBLIC_KEY")), nil
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func usageError() error {
	return errors.New("usage: dlctl verify DIR | import [--publish] INCOMING_ID | publish ID | restore-published INCOMING_ID | list | migrate-tools-layout [--dry-run]")
}

func detail(err error) string {
	payload := map[string]string{}
	if err != nil {
		payload["error"] = err.Error()
	}
	data, _ := json.Marshal(payload)
	return string(data)
}
