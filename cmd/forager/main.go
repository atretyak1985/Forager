// forager — web research tools for local LM Studio models.
//
// Usage:
//
//	forager ask "питання"          one-shot research from the CLI
//	forager serve                  OpenAI-compatible proxy with web tools
//
// Config (flags override env):
//
//	LMSTUDIO_URL         default http://localhost:1234/v1
//	SEARXNG_URL          default http://localhost:8888
//	FORAGER_MODEL        default qwen3-14b
//	FORAGER_LISTEN       default 127.0.0.1:8090
//	FORAGER_WORKSPACE    default /srv/forager/workspace
//	FORAGER_SANDBOX      default forager-sandbox
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/swarmery/forager/internal/agent"
	"github.com/swarmery/forager/internal/llm"
	"github.com/swarmery/forager/internal/memory"
	"github.com/swarmery/forager/internal/proxy"
	"github.com/swarmery/forager/internal/sandbox"
	"github.com/swarmery/forager/internal/tools"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	log.SetFlags(log.Ltime)

	var (
		lmURL      string
		searxURL   string
		model      string
		listen     string
		maxIter    int
		fetchChars int
		verbose    bool
		workspace  string
		sandboxCt  string
		profile    string
	)

	fs := flag.NewFlagSet("forager", flag.ExitOnError)
	fs.StringVar(&lmURL, "lm", envOr("LMSTUDIO_URL", "http://localhost:1234/v1"), "LM Studio base URL")
	fs.StringVar(&searxURL, "searx", envOr("SEARXNG_URL", "http://localhost:8888"), "SearXNG base URL")
	fs.StringVar(&model, "model", envOr("FORAGER_MODEL", "qwen3-14b"), "model name as loaded in LM Studio")
	fs.StringVar(&listen, "listen", envOr("FORAGER_LISTEN", "127.0.0.1:8090"), "listen address(es) for serve mode, comma-separated")
	fs.IntVar(&maxIter, "max-iter", 12, "max agent iterations per request")
	fs.IntVar(&fetchChars, "fetch-chars", 12000, "max characters returned per fetch_page call")
	fs.BoolVar(&verbose, "v", false, "log tool calls")
	fs.StringVar(&workspace, "workspace", envOr("FORAGER_WORKSPACE", "/srv/forager/workspace"), "host path of the shared /workspace volume")
	fs.StringVar(&sandboxCt, "sandbox", envOr("FORAGER_SANDBOX", "forager-sandbox"), "sandbox container name")
	fs.StringVar(&profile, "profile", "web", "tool profile for ask mode: web or agent")

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	_ = fs.Parse(os.Args[2:])

	client := llm.New(lmURL)
	ws := &tools.Workspace{Root: workspace}
	mem := &memory.Store{Dir: filepath.Join(workspace, "memory")}
	runner := &sandbox.Docker{Container: sandboxCt, Workdir: "/workspace"}

	research := tools.NewRegistry(
		tools.NewSearch(searxURL, 8),
		tools.NewFetch(fetchChars),
	)
	full := tools.NewRegistry(
		tools.NewSearch(searxURL, 8),
		tools.NewFetch(fetchChars),
		tools.NewShell(runner, 16000),
		tools.NewReadFile(ws, fetchChars),
		tools.NewWriteFile(ws),
		tools.NewListDir(ws),
		tools.NewPython(runner, ws, "/workspace", 16000),
		memory.NewSave(mem),
		memory.NewSearch(mem),
	)

	mkAgent := func(reg *tools.Registry, prompt string, suffix func() string) *agent.Agent {
		ag := agent.New(client, reg, agent.Config{
			Model:         model,
			MaxIterations: maxIter,
			Temperature:   0.2,
			SystemPrompt:  prompt,
			PromptSuffix:  suffix,
		})
		if verbose || cmd == "serve" {
			ag.OnEvent = func(format string, args ...any) { log.Printf(format, args...) }
		}
		return ag
	}
	memIndex := func() string {
		if idx := mem.Index(4000); idx != "" {
			return "Long-term memory index (use memory_search or read_file on memory/<file> for details):\n" + idx
		}
		return ""
	}
	agents := map[string]*agent.Agent{
		"web":   mkAgent(research, "", nil),
		"agent": mkAgent(full, agent.AgentSystemPrompt, memIndex),
	}

	switch cmd {
	case "ask":
		question := strings.TrimSpace(strings.Join(fs.Args(), " "))
		if question == "" {
			fmt.Fprintln(os.Stderr, "usage: forager ask [flags] \"your question\"")
			os.Exit(2)
		}
		ag, ok := agents[profile]
		if !ok {
			log.Fatalf("unknown profile %q (want web or agent)", profile)
		}
		runAsk(ag, question)

	case "serve":
		runServe(agents, model, listen)

	default:
		usage()
		os.Exit(2)
	}
}

func runAsk(ag *agent.Agent, question string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	answer, err := ag.Ask(ctx, question)
	if err != nil {
		log.Fatalf("research failed: %v", err)
	}
	fmt.Println(answer)
}

func runServe(agents map[string]*agent.Agent, model, listen string) {
	srv := &proxy.Server{Agents: agents, DefaultModel: model}
	handler := srv.Handler()

	var servers []*http.Server
	for _, addr := range strings.Split(listen, ",") {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		httpSrv := &http.Server{Addr: addr, Handler: handler}
		servers = append(servers, httpSrv)
		go func(a string, s *http.Server) {
			log.Printf("forager serving OpenAI-compatible API on http://%s/v1 (profiles web+agent, default model %q)", a, model)
			if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server %s: %v", a, err)
			}
		}(addr, httpSrv)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	log.Println("shutting down")
	for _, s := range servers {
		_ = s.Close()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `forager — web research tools for local LM Studio models

commands:
  ask "question"   run a one-shot research query
  serve            start OpenAI-compatible proxy with web tools

flags (after the command):
  -lm URL          LM Studio base URL      (env LMSTUDIO_URL, default http://localhost:1234/v1)
  -searx URL       SearXNG base URL        (env SEARXNG_URL,  default http://localhost:8888)
  -model NAME      model in LM Studio      (env FORAGER_MODEL,  default qwen3-14b)
  -listen ADDRS    serve listen address(es), comma-sep    (env FORAGER_LISTEN, default 127.0.0.1:8090)
  -max-iter N      max agent iterations    (default 12)
  -fetch-chars N   max chars per page read (default 12000)
  -v               verbose tool logging
  -workspace DIR   host path of /workspace volume  (env FORAGER_WORKSPACE, default /srv/forager/workspace)
  -sandbox NAME    sandbox container name          (env FORAGER_SANDBOX, default forager-sandbox)
  -profile NAME    ask-mode tool profile: web|agent (default web)`)
}
