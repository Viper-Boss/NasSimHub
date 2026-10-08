package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/modemdeck/internal/store"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type readOnlyRepository struct{ *store.Store }

func (r readOnlyRepository) TouchNasSimHubNode(context.Context, string, string, string, string, time.Time) error {
	return nil
}
func (r readOnlyRepository) RecordNasSimHubNodeSIM(context.Context, store.NasSimHubNodeSIM) error {
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: nsh-admin-check existing-data-directory")
	}
	dir := os.Args[1]
	identityDir := filepath.Join(dir, "nassimhub")
	if _, err := os.Stat(filepath.Join(identityDir, nassimhubnode.IdentityFileName)); err != nil {
		return err
	}
	identity, err := nassimhubnode.LoadOrCreateIdentity(identityDir)
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "modemdeck.db")+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	repo, err := store.New(db)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fleet, err := nassimhubnode.New(ctx, nassimhubnode.Options{Identity: identity, Repository: readOnlyRepository{repo}, CoreVersion: "1.15.0", Timeout: 8 * time.Second})
	if err != nil {
		return err
	}
	fleet.PollAll(ctx)
	checked := 0
	for _, node := range fleet.Nodes() {
		if !node.Online {
			continue
		}
		result, err := fleet.LocalManagement(ctx, node.DeviceID)
		if err != nil {
			return err
		}
		u, err := url.Parse(result.URL)
		if err != nil {
			return err
		}
		values, err := url.ParseQuery(u.Fragment)
		if err != nil {
			return err
		}
		proof := values.Get("setup")
		if u.Scheme != "https" || u.Port() != "7581" || u.RawQuery != "" || u.User != nil {
			return fmt.Errorf("unsafe management link")
		}
		if result.Configured && proof != "" || !result.Configured && len(proof) != 43 {
			return fmt.Errorf("incorrect setup state")
		}
		fmt.Printf("PASS paired owner access: device=%s configured=%v management=%s://%s proof_is_fragment_only=%v\n", node.DeviceID, result.Configured, u.Scheme, u.Host, !result.Configured)
		checked++
	}
	if checked == 0 {
		return fmt.Errorf("no online paired node")
	}
	return nil
}
