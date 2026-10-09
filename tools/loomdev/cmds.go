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

	"github.com/aidan-bailey/loom/internal/daemon"
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
			if rec, held := sb.Daemon(); held {
				fmt.Fprintf(a.out, "the sandbox's daemon (pid %d) runs the build it started with: the next loom to start replaces it when this build is newer (a later commit, or an edited tree rebuilt, in a git checkout), and refuses an older one (`loomdev stop` first)\n", rec.PID)
			}
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
			err = c.Run()
			// As after a real loom, the daemon it started keeps running.
			if rec, held := sb.Daemon(); held {
				fmt.Fprintf(a.errOut, "loomdev: the sandbox's daemon keeps running (%s); `loomdev stop` ends it\n", daemonText(rec))
			}
			if err != nil {
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
			fmt.Fprintf(a.out, "driver running on tmux -L %s (session %s)\n", sb.Socket(), sb.Driver())
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
	var keepDaemon bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Quit the headless loom, remove the driver session and stop the sandbox's daemon",
		Long: `Quit the headless loom, remove the driver session and stop the sandbox's
daemon, so the next start boots a fresh daemon that reattaches the sessions
(the restore path). The daemon serves every driver and ` + "`loomdev run`" + `, so
stopping it ends their TUIs too. --keep-daemon leaves it running, as a real
quit does: the next start connects to the same daemon.`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			if keepDaemon {
				return sb.StopDriver(grace)
			}
			return sb.Stop(grace)
		},
	}
	cmd.Flags().DurationVar(&grace, "grace", 5*time.Second, "how long to wait for loom to quit before killing it")
	cmd.Flags().BoolVar(&keepDaemon, "keep-daemon", false, "quit the TUI only, leaving the sandbox's daemon running")
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
		Short: "Show the sandbox's daemon and its loom.log and serve.log files",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			if rec, held := sb.Daemon(); held {
				fmt.Fprintf(a.out, "daemon: %s\n", daemonText(rec))
			} else {
				fmt.Fprintln(a.out, "daemon: not running")
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
			fmt.Fprintln(tw, "NAME\tSERVER\tDAEMON\tSOCKET\tBUILD\tSOURCE")
			for _, in := range infos {
				server, serving, socket, build, source := "down", "down", "-", "-", "-"
				if in.ServerAlive {
					server = "up"
				}
				if d := in.Daemon; d != nil {
					serving, socket = fmt.Sprintf("pid %d", d.PID), d.Socket
					if !d.IsDaemon() {
						socket = "(starting)"
					}
				}
				if in.Meta != nil {
					build, source = in.Meta.BuildSHA, in.Meta.SourceWorktree
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", in.Name, server, serving, socket, build, source)
			}
			return tw.Flush()
		},
	}
}

func (a *app) downCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Stop the sandbox's daemon, kill its tmux server and delete the sandbox",
		Long: `Stop the sandbox's daemon, kill its tmux server and delete the sandbox.
A daemon that won't stop, or a loom from before the daemon holding the
sandbox's lock, stops down with nothing removed; --force kills that process
(SIGKILL) first, once it is proved a build in the sandbox's bin dir.`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sb, err := a.open()
			if err != nil {
				return err
			}
			down := sb.Down
			if force {
				down = sb.ForceDown
			}
			if err := down(); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "removed %s\n", sb.Dir)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "kill (SIGKILL) a sandbox loom that holds the lock and won't stop")
	return cmd
}

// daemonText describes a sandbox daemon from its lock record: "pid 4242
// on /run/user/1000/loom/1f2e….sock", or still starting.
func daemonText(rec daemon.Record) string {
	if !rec.IsDaemon() {
		return fmt.Sprintf("pid %d, starting", rec.PID)
	}
	return fmt.Sprintf("pid %d on %s", rec.PID, rec.Socket)
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
