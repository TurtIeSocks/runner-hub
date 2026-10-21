// Command runner-hub starts single-use GitHub Actions runners on a pool of
// boxes for the queued jobs of opted-in repos.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/TurtIeSocks/runner-hub/internal/box"
	"github.com/TurtIeSocks/runner-hub/internal/config"
	"github.com/TurtIeSocks/runner-hub/internal/dispatch"
	"github.com/TurtIeSocks/runner-hub/internal/gh"
)

const usage = `usage: runner-hub [-config path] [-debug] <command> [args]

commands:
  run              poll and dispatch until stopped (the service)
  once             one reconcile, then exit
  status           print each box's state and each repo's hub runners
  prepare [box..]  ready boxes (dirs, runner image); all boxes if none named
`

func main() {
	cfgPath := flag.String("config", "/etc/runner-hub/config.toml", "config file")
	debug := flag.Bool("debug", false, "debug logging")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fail(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	switch args[0] {
	case "run", "once":
		client, err := gh.New(cfg.AppID, cfg.PrivateKeyPath)
		if err != nil {
			fail(err)
		}
		d := dispatch.New(cfg, client, log)
		if args[0] == "once" {
			d.Reconcile(ctx)
			return
		}
		log.Info("runner-hub started", "repos", cfg.Repos, "boxes", len(cfg.Boxes), "poll", cfg.PollInterval, "idle_timeout", cfg.IdleTimeout)
		d.Run(ctx)
		log.Info("runner-hub stopped")
	case "status":
		client, err := gh.New(cfg.AppID, cfg.PrivateKeyPath)
		if err != nil {
			fail(err)
		}
		status(ctx, cfg, client)
	case "prepare":
		if !prepare(ctx, cfg, args[1:]) {
			os.Exit(1)
		}
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func status(ctx context.Context, cfg *config.Config, client *gh.Client) {
	for _, bc := range cfg.Boxes {
		st, err := box.New(bc, cfg).Status(ctx)
		if err != nil {
			fmt.Printf("%-10s DOWN %v\n", bc.Name, err)
			continue
		}
		var cs []string
		for _, c := range st.Containers {
			cs = append(cs, fmt.Sprintf("%s(%s, %s)", c.Name, c.Repo, c.State))
		}
		fmt.Printf("%-10s %-6s claimed=%-5v %d/%d %s\n", bc.Name, bc.Arch, st.Claimed, len(st.Containers), bc.Slots, strings.Join(cs, " "))
	}
	for _, r := range cfg.Repos {
		private, err := client.IsPrivate(ctx, r)
		if err != nil {
			fmt.Printf("%s: %v\n", r, err)
			continue
		}
		rs, err := client.Runners(ctx, r)
		if err != nil {
			fmt.Printf("%s: %v\n", r, err)
			continue
		}
		var names []string
		for _, x := range rs {
			names = append(names, fmt.Sprintf("%s(%s busy=%v)", x.Name, x.Status, x.Busy))
		}
		fmt.Printf("%s private=%v runners: %s\n", r, private, strings.Join(names, " "))
	}
}

func prepare(ctx context.Context, cfg *config.Config, only []string) bool {
	ok := true
	for _, bc := range cfg.Boxes {
		if len(only) > 0 && !slices.Contains(only, bc.Name) {
			continue
		}
		report, err := box.New(bc, cfg).Prepare(ctx, cfg.RunnerImage)
		if err != nil {
			fmt.Printf("%-10s FAILED %v\n", bc.Name, err)
			ok = false
			continue
		}
		fmt.Printf("%-10s %s\n", bc.Name, report)
	}
	return ok
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "runner-hub:", err)
	os.Exit(1)
}
