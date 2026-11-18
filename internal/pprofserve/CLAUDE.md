# internal/pprofserve

Opt-in runtime profiles: when `LOOM_PPROF` asks (`1`, or a loopback `host:port`), `FromEnv` serves the process's `runtime/pprof` profiles over HTTP on loopback and logs the address as `pprof.listening`. `serve.go` starts one for the daemon and `main.go`'s root command one for each TUI; `Close` on the nil `*Server` returned while it is off does nothing. Routes: `/debug/pprof/` (an index), `/debug/pprof/profile?seconds=N` (CPU, 1 to 300, default 30), `/debug/pprof/<name>` (every `pprof.Profiles()` name; `?debug=N` for text, `?gc=1` before a heap).

## Rules when modifying this package

- **Serve only on loopback, and refuse rather than fall back: `Parse` rejects a non-loopback host or an empty one (`:6060` binds every interface), `Start` closes a listener that bound off loopback, and `FromEnv` logs `pprof.refused` and serves nothing.** A goroutine dump carries argument values and a heap profile the process's allocation sites; on another interface any host on the network reads them, silently. **Enforced** by `TestParse`, `TestStart_RefusesAnAddressOffLoopback` and `TestFromEnv_RefusedServesNothing`.
- **Answer only a request whose `Host` is loopback (`loopbackOnly`).** A page the user's browser loaded can reach a loopback port through DNS rebinding, and its requests carry the page's host name. **Enforced** by `TestServer_RefusesAForeignHost`.
- **Never import `net/http/pprof`, here or anywhere in loom: build handlers on `runtime/pprof`.** Its `init` registers the profiler on `http.DefaultServeMux`, so any server later built with a nil handler serves it too, the web layer's loopback port included, which a Cloudflare tunnel exposes. **Convention** — profiles readable from the internet, silently.
- **Start a server only in the daemon and the TUI, never in a subcommand or the job shim.** A daemon that started the tmux server gives agents its environment, so `LOOM_PPROF` there would make every agent's `loom job` and `loom session` call open a listener. **Convention** — a port per short-lived command.

## Pointers

- [`../../docs/claude/debugging-freezes.md`](../../docs/claude/debugging-freezes.md) — profiling a busy TUI or daemon, with perf for builds that predate this package.
- [`../../docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md) — where this package sits.
