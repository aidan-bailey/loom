# internal/web

The daemon's web layer: what `loom serve` starts so a phone can see the fleet and act on it. It is a client of the daemon's own `rpc.Server`, joined over `net.Pipe` as the `web` role, plus `core.WebHost` for the device records the model keeps in `web.json`. It serves an embedded app, a JSON API and a live stream on a loopback port that a Cloudflare tunnel reaches, and sends Web Push. It holds no session state, and logs under `subsystem=web`. Rules for changing it: [`CLAUDE.md`](CLAUDE.md).

## Files

| File | Holds |
|---|---|
| `web.go` | `Options`, `Start`, `Layer` and `Close`; the drain; `fail` and `recoverPanic`; the `sink` and `mux` seams each part registers through |
| `server.go` | the listener (`bind`), `guard` and the origin rule |
| `auth.go` | pairing (`routePair`), `authed`, the device cookie |
| `jsonio.go` | `readJSON`, `writeJSON`, `writeError` |
| `testmain_test.go` | isolates the loom dirs and tmux for the package's tests |

## How it runs

`daemon.Serve` calls `web.Start` after the mod server listens and `Layer.Close` first when it stops, before the bye. `Start` joins the server as the `web` role, which the server does not count as a client, so a notice raised while only the layer is connected waits for the next TUI. It builds its parts (`startParts`) and then drains the client as the TUI does: on each wake it takes `Sync`'s events, hands them to each sink in turn, and applies the `WebView` a `ModelChanged` carries.

The layer listens only while `WebView.Listen` is set. It acts on `WebView.Rev`, which the model bumps on every `ConfigureWeb` and on nothing else: whenever `Rev` differs from the last one the layer acted on, it closes the listener, which ends every stream, and binds `Listen` unless it is empty (then it stays closed). Two `ConfigureWeb`s that one `Sync` coalesces still differ from that `Rev`, so a disable and an enable are never missed. What happened goes back to the model through `WebHost.WebServing`, which leaves `Rev` alone, so a report never binds. A listen that fails (the port is taken) leaves the web off with the reason in `WebView.Error` and a notice for the next TUI, and is tried again only by the next `ConfigureWeb`, the same values included: `loom web enable` again. The listener takes loopback IP literals only, whatever `web.json` says.

A panic in the drain or the listener stops the layer, tells the model (the same notice) and leaves the daemon running. Each goroutine the layer starts defers `recoverPanic` first, which calls `fail`; `fail` returns at once and stops the layer on a goroutine of its own, so a part can call it from the goroutine that panicked. `Close` runs the same stop, once between them: whichever comes first stops the layer, and the other waits until it is complete. `Close` still has to run at the daemon's stop.

## Every request

`guard` stands in front of every route:

1. The security headers go on the answer, errors included.
2. The `Host` must be the origin's host (default port dropped, any case), else 421.
3. A method other than GET or HEAD must carry exactly one `Origin` header equal to the origin, else 403.
4. The body is capped at `maxBody` (1 MiB).

With no listener bound there is no origin rule, and `guard` refuses everything.

## Pairing and the device cookie

`POST /api/v1/pair` takes `{"code": "K7QM-2XPA"}` and asks the host to redeem it. The model checks the code, consumes it and makes the device; the layer sets the token it returns as the cookie `__Host-loom` (Secure, HttpOnly, SameSite=Strict, Path=/) and answers `{"device": "<name>"}`. The refusals are `{"error": <text>}` with a status the app keys on: a wrong, used or expired code is 400 (never 401, which the app reads as "not paired"), too many wrong codes 429, a name already paired or 16 devices paired 409, and a web that is off 503; any other failure is a 500 with a fixed text.

`authed` wraps every route after pairing: it reads the cookie, decodes it as unpadded base64url, refuses (401, without asking the host) a value that is not exactly 32 bytes, and asks the host which device holds the SHA-256 of those 32 bytes. No device is a 401; otherwise the device is touched (last seen) and the handler runs with its name.

## Tests

The tests boot a model on its loop, serve it with the real `rpc.Server`, start the layer on it and talk to it over HTTP on a loopback port (`webStack` in `web_test.go`). `TestMain` points the loom dirs and tmux at throwaway ones.
