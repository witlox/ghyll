package main

import (
	gocontextpkg "context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/witlox/ghyll/memory"
)

// cmdMemoryMain handles `ghyll memory` subcommands.
func cmdMemoryMain(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ghyll memory [log|search <query>|sync]")
	}

	dbPath := filepath.Join(os.Getenv("HOME"), ".ghyll", "memory.db")
	store, err := memory.OpenStore(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	switch args[0] {
	case "log":
		return cmdMemoryLog(store, os.Stdout)
	case "search":
		if len(args) < 2 {
			return fmt.Errorf("usage: ghyll memory search <query>")
		}
		query := strings.Join(args[1:], " ")
		return cmdMemorySearch(store, query, os.Stdout)
	case "sync":
		return cmdMemorySyncManual()
	default:
		return fmt.Errorf("unknown memory command: %s", args[0])
	}
}

// cmdMemorySyncManual triggers a manual sync of the memory branch.
func cmdMemorySyncManual() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	hostname, _ := os.Hostname()
	deviceID := hostname
	if deviceID == "" {
		deviceID = "default"
	}

	branch := "ghyll/memory"
	syncer, err := memory.NewSyncer(cwd, branch, deviceID)
	if err != nil {
		return fmt.Errorf("sync setup: %w", err)
	}

	fmt.Println("fetching remote checkpoints...")
	if err := syncer.Fetch(); err != nil {
		fmt.Printf("fetch: %v (continuing with push)\n", err)
	}

	gocontext := gocontextpkg.Background()
	fmt.Println("pushing local checkpoints...")
	if err := syncer.CommitAndPush(gocontext); err != nil {
		return fmt.Errorf("push: %w", err)
	}

	fmt.Println("sync complete")
	return nil
}

// cmdMemoryLog shows the checkpoint chain across all sessions.
func cmdMemoryLog(store *memory.Store, w io.Writer) error {
	checkpoints, err := store.ListAll()
	if err != nil {
		return err
	}

	if len(checkpoints) == 0 {
		_, _ = fmt.Fprintln(w, "no checkpoints")
		return nil
	}

	for _, cp := range checkpoints {
		ts := time.Unix(0, cp.Timestamp)
		if cp.Timestamp < 1e12 {
			// Treat as unix seconds if too small for nanos
			ts = time.Unix(cp.Timestamp, 0)
		}
		_, _ = fmt.Fprintf(w, "%s  %s  [%s] @%s  turn %d  %s\n",
			cp.Hash[:12],
			ts.Format("2006-01-02 15:04"),
			cp.ActiveModel,
			cp.AuthorID,
			cp.Turn,
			cp.Summary,
		)
	}
	return nil
}

// cmdMemorySearch searches checkpoint summaries for matching terms.
// Uses text matching when embedder is unavailable, cosine similarity when available.
func cmdMemorySearch(store *memory.Store, query string, w io.Writer) error {
	checkpoints, err := store.ListAll()
	if err != nil {
		return err
	}

	queryLower := strings.ToLower(query)
	queryTerms := strings.Fields(queryLower)

	var matches []memory.Checkpoint
	for _, cp := range checkpoints {
		summaryLower := strings.ToLower(cp.Summary)
		matched := 0
		for _, term := range queryTerms {
			if strings.Contains(summaryLower, term) {
				matched++
			}
		}
		// Match if at least half the query terms are found
		if matched > 0 && matched >= len(queryTerms)/2 {
			matches = append(matches, cp)
		}
	}

	if len(matches) == 0 {
		_, _ = fmt.Fprintln(w, "no matching checkpoints")
		return nil
	}

	for _, cp := range matches {
		ts := time.Unix(0, cp.Timestamp)
		if cp.Timestamp < 1e12 {
			ts = time.Unix(cp.Timestamp, 0)
		}
		_, _ = fmt.Fprintf(w, "%s  %s  @%s  %s\n",
			cp.Hash[:12],
			ts.Format("2006-01-02 15:04"),
			cp.AuthorID,
			cp.Summary,
		)
	}
	return nil
}
