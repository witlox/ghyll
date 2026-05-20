package main

import (
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	gocontext "context"

	"github.com/witlox/ghyll/cmd/ghyll/modal"
	ghyllcontext "github.com/witlox/ghyll/context"
	"github.com/witlox/ghyll/ui"
)

// REPL runs the interactive read-eval-print loop.
//
// Gate-2 CONC-C-1/C-2: the REPL no longer constructs its own
// bufio.Scanner. It uses the session's shared LineReader so the
// modal (which pulls from the SAME reader) can interleave reads
// without losing buffered bytes. When the session has no shared
// reader (test wiring with a custom modalPrompt + injected input),
// the REPL constructs a per-call LineReader and closes it on
// EOF — same single-scanner invariant locally.
func REPL(sess *Session, input io.Reader) {
	// Construct the shared LineReader over `input`. If the session
	// already has one (test that pre-installed sess.lines), reuse
	// it. Otherwise build a fresh reader + (in production) wire the
	// session's TermModal to share it so the modal and REPL pull
	// from the same scanner. Gate-2 CONC-C-1/C-2.
	var reader *modal.LineReader
	if sess.lines != nil {
		reader = sess.lines
	} else {
		reader = modal.NewLineReader(input)
		sess.lines = reader
		if tm, ok := sess.modalPrompt.(*modal.TermModal); ok && tm.Lines == nil {
			tm.Lines = reader
		}
		defer func() {
			reader.Close()
			sess.lines = nil
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		ui.Info("\nshutting down...")
		// Create final checkpoint on graceful shutdown
		if sess.ctxManager.Turn() > 0 {
			_ = sess.createCheckpoint(ghyllcontext.CheckpointRequest{
				SessionID:   sess.sessionID,
				Turn:        sess.ctxManager.Turn(),
				ActiveModel: sess.activeModel,
				Summary:     "session ended (signal)",
				Messages:    sess.ctxManager.Messages(),
				Reason:      "shutdown",
			})
		}
		// Gate-2 CONC-M-4: signal handler now calls sess.Close
		// (which cancels sessionCtx, unsubscribes the modal
		// driver, closes the line reader, drains the journal,
		// and closes the engine) before os.Exit. The previous
		// path went straight to os.Exit, leaking in-flight
		// goroutines + losing journal-buffered events.
		sess.Close()
		os.Exit(0)
	}()

	for {
		// Tier 2 Step 8 + 10 (ADR-016 Part D / gate-1 F-14):
		// drain any queued operator verdict / escalation modals
		// BEFORE re-prompting. Uses the session-scoped ctx so
		// /exit can cancel a blocked modal read cleanly.
		sess.DrainModalPending(sess.SessionContext())

		ui.Print(sess.Prompt())

		raw, err := reader.Next(sess.SessionContext())
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, gocontext.Canceled) {
				return
			}
			sess.output("repl read error: " + err.Error())
			return
		}
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		// Built-in slash commands. /quit is a REPL-only alias for /exit.
		if line == "/quit" {
			line = "/exit"
		}
		if res := sess.DispatchSlashCommand(line); res.Handled {
			if res.ExitRequested {
				if sess.ctxManager.Turn() > 0 {
					_ = sess.createCheckpoint(ghyllcontext.CheckpointRequest{
						SessionID:   sess.sessionID,
						Turn:        sess.ctxManager.Turn(),
						ActiveModel: sess.activeModel,
						Summary:     "session ended",
						Messages:    sess.ctxManager.Messages(),
						Reason:      "shutdown",
					})
				}
				ui.Info("goodbye")
				return
			}
			if res.Output != "" {
				sess.output(res.Output)
			}
			continue
		}

		// Non-built-in slash commands (workflow-defined).
		if strings.HasPrefix(line, "/") {
			cmdName := strings.TrimPrefix(line, "/")
			if sess.wf != nil {
				if content, ok := sess.wf.Commands[cmdName]; ok {
					// Inject command content as user input and process as a turn
					_, err := sess.Turn(content)
					if err != nil {
						sess.renderer.RenderError(err.Error())
					}
					continue
				}
			}
			sess.output("unknown command: " + line)
			continue
		}

		// Execute turn — response is already streamed to terminal via renderer
		_, terr := sess.Turn(line)
		if terr != nil {
			sess.renderer.RenderError(terr.Error())
			continue
		}
	}
}
