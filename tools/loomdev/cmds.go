package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/internal/devsandbox"
	"github.com/spf13/cobra"
)

func (a *app) upCmd() *cobra.Command {
	var realClaude bool
	var profile string
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Create or top up the sandbox, then build loom into it",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			root, err := moduleRoot()
			if err != nil {
				return err
			}
			if err := sb.Up(devsandbox.UpOptions{SourceWorktree: root, RealClaude: realClaude, DefaultProfile: profile, Warn: a.errOut}); err != nil {
				return err
			}
			if err := sb.Build(root); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "sandbox %s ready at %s\n", sb.Name, sb.Dir)
			return nil
		},
	}
	cmd.Flags().BoolVar(&realClaude, "real-claude", false, "add a `claude` profile running the real CLI (sticky)")
	cmd.Flags().StringVar(&profile, "default-profile", "", "default profile: fake, fake-claude, fake-aider, shell, claude (rewrites config.json)")
	return cmd
}

func (a *app) buildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build",
		Short: "Rebuild loom and fakeagent into the sandbox",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sb, err := a.prepare(false)
			if err != nil {
				return err
			}
			meta, err := sb.LoadMeta()
			if err != nil {
				return err
			}
			fmt.Fprintf(a.out, "built %s into %s\n", meta.BuildSHA, sb.BinDir())
			return nil
		},
	}
}

func (a *app) runCmd() *cobra.Command {
	var noBuild bool
	cmd := &cobra.Command{
		Use:   "run [-- loom-args...]",
		Short: "Run the sandboxed loom interactively in this terminal",
		RunE: func(_ *cobra.Command, args []string) error {
			sb, err := a.prepare(noBuild)
			if err != nil {
				return err
			}
			c := exec.Command(sb.LoomBin(), append([]string{"--workspace", devsandbox.WorkspaceName}, args...)...)
			c.Dir = sb.RepoDir()
			c.Env = sb.Environ()
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := c.Run(); err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					return &exitError{code: ee.ExitCode()}
				}
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noBuild, "no-build", false, "skip rebuilding")
	return cmd
}

func (a *app) startCmd() *cobra.Command {
	var noBuild, restart bool
	var size string
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the sandboxed loom headlessly in the driver session",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			w, h, err := parseSize(size)
			if err != nil {
				return err
			}
			sb, err := a.prepare(noBuild)
			if err != nil {
				return err
			}
			if err := sb.Start(devsandbox.StartOptions{Width: w, Height: h, Restart: restart}); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "driver running on tmux -L %s (session %s)\n", sb.Socket(), devsandbox.DriverSession)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noBuild, "no-build", false, "skip rebuilding")
	cmd.Flags().BoolVar(&restart, "restart", false, "replace a running driver")
	cmd.Flags().StringVar(&size, "size", "160x48", "driver pane size WIDTHxHEIGHT")
	return cmd
}

func (a *app) stopCmd() *cobra.Command {
	var grace time.Duration
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Quit the headless loom and remove the driver session",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			return sb.Stop(grace)
		},
	}
	cmd.Flags().DurationVar(&grace, "grace", 5*time.Second, "how long to wait for loom to quit before killing it")
	return cmd
}

func (a *app) keysCmd() *cobra.Command {
	var literal bool
	cmd := &cobra.Command{
		Use:   "keys KEY...",
		Short: "Send tmux key names (Enter, Escape, Up, C-c, n …) to the headless loom",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			if literal {
				return sb.SendText(strings.Join(args, " "))
			}
			return sb.SendKeys(args...)
		},
	}
	cmd.Flags().BoolVarP(&literal, "literal", "l", false, "type the arguments as literal text")
	return cmd
}

func (a *app) shotCmd() *cobra.Command {
	var ansi bool
	cmd := &cobra.Command{
		Use:   "shot",
		Short: "Print the headless loom's screen",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			screen, err := sb.Screen(ansi)
			if err != nil {
				return err
			}
			fmt.Fprint(a.out, screen)
			return nil
		},
	}
	cmd.Flags().BoolVar(&ansi, "ansi", false, "keep colors and attributes")
	return cmd
}

func (a *app) waitCmd() *cobra.Command {
	var text string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait --text TEXT",
		Short: "Wait until TEXT appears on the headless loom's screen",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if text == "" {
				return errors.New("--text must not be empty")
			}
			sb, err := a.open()
			if err != nil {
				return err
			}
			err = sb.WaitFor(text, timeout)
			var timedOut *devsandbox.WaitTimeoutError
			if errors.As(err, &timedOut) {
				fmt.Fprintf(a.out, "--- last screen ---\n%s\n--- sandbox logs ---\n%s", timedOut.Screen, sb.TailLogs(20))
			}
			return err
		},
	}
	cmd.Flags().StringVar(&text, "text", "", "text to wait for (required)")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "how long to wait")
	_ = cmd.MarkFlagRequired("text")
	return cmd
}

func (a *app) logsCmd() *cobra.Command {
	var lines int
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show the sandbox's loom.log files",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			fmt.Fprint(a.out, sb.TailLogs(lines))
			if !follow {
				return nil
			}
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt)
			defer stop()
			return followLogs(ctx, a.out, sb.LogFiles(), 250*time.Millisecond)
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "lines per file")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new lines until interrupted")
	return cmd
}

func (a *app) envCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "env",
		Short: "Print export lines for the sandbox environment (eval \"$(loomdev env)\")",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			for _, kv := range sb.Env() {
				k, v, _ := strings.Cut(kv, "=")
				fmt.Fprintf(a.out, "export %s=%s\n", k, devsandbox.ShellQuote(v))
			}
			return nil
		},
	}
}

func (a *app) lsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List sandboxes",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			infos, err := devsandbox.List()
			if err != nil {
				return err
			}
			if len(infos) == 0 {
				fmt.Fprintln(a.out, "no sandboxes")
				return nil
			}
			tw := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tSERVER\tBUILD\tSOURCE")
			for _, in := range infos {
				server, build, source := "down", "-", "-"
				if in.ServerAlive {
					server = "up"
				}
				if in.Meta != nil {
					build, source = in.Meta.BuildSHA, in.Meta.SourceWorktree
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", in.Name, server, build, source)
			}
			return tw.Flush()
		},
	}
}

func (a *app) downCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Kill the sandbox's tmux server and delete the sandbox",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			if err := sb.Down(); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "removed %s\n", sb.Dir)
			return nil
		},
	}
}

func parseSize(s string) (int, int, error) {
	ws, hs, ok := strings.Cut(s, "x")
	w, errW := strconv.Atoi(ws)
	h, errH := strconv.Atoi(hs)
	if !ok || errW != nil || errH != nil || w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("invalid size %q (want WIDTHxHEIGHT, e.g. 160x48)", s)
	}
	return w, h, nil
}
