# internal/web

The daemon's web layer, started by `loom serve`: a client of the daemon's own `rpc.Server` plus `core.WebHost`, serving the loom PWA on a loopback port that a Cloudflare tunnel reaches (pairing, a JSON API, a live stream, Web Push). It holds no session state: the model owns every session and every device record.

## Rules when modifying this package

- **Decode a request body only with `readJSON`, and answer only with `writeJSON` and `writeError`.** `readJSON` refuses an empty body, a field the route doesn't take, a second value and a body past the cap, so a misspelt field is a 400 rather than a silently ignored value; the writers encode before the header, so a value that won't encode is a 500 with a fixed body, never a 200 with half of one. **Enforced** by `TestReadJSON` and `TestWriteJSON_AValueThatWillNotEncodeIsA500`.
