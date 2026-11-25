package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/joho/godotenv"

	"sofra/internal/action"
	"sofra/internal/agent"
	"sofra/internal/clock"
	"sofra/internal/confirm"
	"sofra/internal/domain"
	"sofra/internal/knowledge"
	"sofra/internal/policy"
	"sofra/internal/store"
	"sofra/internal/tools"
	"sofra/internal/ui"
)

const (
	dataDir       = "data"
	knowledgePath = "data/knowledge/kb.jsonl"
	schemaPath    = "schema/ui_spec.schema.json"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	go func() {
		<-ctx.Done()
		stop()
	}()
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "sofra:", err)
		os.Exit(1)
	}
}

const usage = "usage: sofra chat [-user <user_id>] | sofra scenarios [-only 3,14] [-trace <dir>] | sofra eval [-only ev_05,ev_12]"

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "chat":
		return runChat(ctx, args[1:])
	case "scenarios":
		return runScenarios(ctx, args[1:])
	case "eval":
		return runEval(ctx, args[1:])
	default:
		return errors.New(usage)
	}
}

func runChat(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("chat", flag.ContinueOnError)
	userID := flags.String("user", "u_ok", "user to sign in as")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	a, err := build(cfg, setup{clock: clock.NewRunning(cfg.now), tracePath: cfg.tracePath})
	if err != nil {
		return err
	}
	defer a.close()
	if _, err := a.store.User(domain.UserID(*userID)); err != nil {
		return err
	}
	return a.chat(ctx, agent.NewSession(domain.UserID(*userID)))
}

type config struct {
	apiKey    string
	now       time.Time
	tracePath string
}

func loadConfig() (config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return config{}, fmt.Errorf(".env: %w", err)
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return config{}, errors.New("ANTHROPIC_API_KEY is not set (in the environment or .env)")
	}
	now, err := clock.ParseReference(os.Getenv("SOFRA_NOW"))
	if err != nil {
		return config{}, err
	}
	return config{apiKey: apiKey, now: now, tracePath: os.Getenv("SOFRA_TRACE")}, nil
}

type app struct {
	store    *store.Store
	agent    *agent.Agent
	renderer *ui.Renderer
	trace    *os.File
}

type setup struct {
	clock     clock.Clock
	tracePath string
}

func build(cfg config, su setup) (*app, error) {
	clk := su.clock
	st, err := store.Load(dataDir)
	if err != nil {
		return nil, err
	}
	kb, err := knowledge.Load(knowledgePath)
	if err != nil {
		return nil, err
	}
	if err := policy.VerifyAgainstKnowledge(kb.CurrentPolicy); err != nil {
		return nil, err
	}

	secret, err := confirm.RandomSecret()
	if err != nil {
		return nil, err
	}
	tokens, err := confirm.NewService(secret, clk)
	if err != nil {
		return nil, err
	}
	renderer, err := ui.NewRenderer(schemaPath)
	if err != nil {
		return nil, err
	}
	trace, err := openTrace(su.tracePath)
	if err != nil {
		return nil, err
	}

	actions := action.NewService(st, tokens, clk)
	exec := tools.NewExecutor(st, kb, actions)
	client := anthropic.NewClient(option.WithAPIKey(cfg.apiKey))
	var traceW io.Writer
	if trace != nil {
		traceW = trace
	}
	return &app{store: st, agent: agent.New(client, exec, traceW), renderer: renderer, trace: trace}, nil
}

func (a *app) close() {
	if a.trace != nil {
		a.trace.Close()
	}
}

func openTrace(path string) (*os.File, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("trace: %w", err)
	}
	return f, nil
}

func (a *app) chat(ctx context.Context, s *agent.Session) error {
	fmt.Fprintf(os.Stderr, "Signed in as %s. Type a message, /confirm <token> to press a confirmation's button, or exit.\n", s.UserID)
	in := bufio.NewScanner(os.Stdin)
	lines := make(chan string)
	go func() {
		defer close(lines)
		for in.Scan() {
			lines <- in.Text()
		}
	}()

	for {
		fmt.Fprint(os.Stderr, "> ")
		var text string
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr)
			return nil
		case line, ok := <-lines:
			if !ok {
				return in.Err()
			}
			text = strings.TrimSpace(line)
		}
		switch text {
		case "":
			continue
		case "exit":
			return nil
		}

		// "/confirm <token>" plays the part of the confirm button on the
		// confirmation screen. The token goes straight to the trusted
		// layer and never reaches the model.
		var resp ui.Response
		var err error
		if token, ok := strings.CutPrefix(text, "/confirm "); ok {
			resp, err = a.agent.ConfirmOnScreen(s, strings.TrimSpace(token))
		} else {
			resp, err = a.agent.Handle(ctx, s, text)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		out, err := a.renderer.Render(resp)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		var pretty bytes.Buffer
		if json.Indent(&pretty, out, "", "  ") != nil {
			pretty.Write(out)
		}
		fmt.Println(pretty.String())
	}
}
