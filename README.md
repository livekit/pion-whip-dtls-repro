# pion/dtls v3.1.3 regression repro (vs OpenSSL via gst-plugins-bad whipsink)

A WebRTC handshake that succeeds with `github.com/pion/dtls/v3 v3.1.2`
hangs forever with `v3.1.3`. The DTLS peer is `gst-plugins-bad`'s `whipsink`
element, which uses OpenSSL's DTLS implementation (OpenSSL 3.0.13 in the
`livekit/gstreamer:1.24.12-prod-rs` image). ICE reaches `connected` either
way; pion's DTLS state machine never advances to flight5 in v3.1.3.

## Topology

```
gst-launch (whipsink)               this Go server (pion/webrtc)
  WHIP publisher                      WHIP receiver
  DTLS server (OpenSSL)  ←—DTLS—→    DTLS client (pion/dtls)
```

## How to run

Requires: Go and Docker. The Docker image is pulled from Docker Hub.

```
./run.sh                    # arm64 host (Apple Silicon)
GOARCH=amd64 ./run.sh       # x86_64 host
```

The script builds the WHIP server twice (once per pion/dtls version) and runs
the gst publisher against each inside the same container, then prints the
server's log.

## Expected output

```
================ pion/dtls v3.1.2 ================
WHIP server listening on :8080 (POST /whip)
incoming offer (1654 bytes)
ICE state: checking
Peer connection state: connecting
sent answer (...)
ICE state: connected
Peer connection state: connected           ← handshake done
OnTrack: kind=audio codec=audio/OPUS
OnTrack: kind=video codec=video/H264

================ pion/dtls v3.1.3 ================
WHIP server listening on :8080 (POST /whip)
incoming offer (1658 bytes)
ICE state: checking
Peer connection state: connecting
sent answer (...)
ICE state: connected
                                            ← stalled; no further logs
```

## Detailed evidence

With `PION_TRACE=1` set, pion's log shows the handshake FSM stuck in Flight 1,
repeatedly retransmitting its initial `ClientHello`:

```
dtls TRACE: [handshake:client] Flight 1: Preparing
dtls TRACE: [handshake:client] Flight 1: Sending
dtls TRACE: [handshake:client] -> ClientHello (epoch: 0, seq: 0)
dtls TRACE: [handshake:client] Flight 1: Waiting
... (1s, 2s, 4s, 8s backoff retransmits, all "Flight 1: Sending") ...
```

A `tcpdump` of UDP traffic on the loopback interface (gst and pion both inside
the container) shows the wire-level picture:

```
49444 > 38794   STUN binding (ICE)         ... ↔ ...    success
49444 > 38794   149 B   ← pion's ClientHello (no cookie)
38794 > 49444   256 B 256 B 256 B 256 B 234 B 98 B
                        ← gst's flight 4 (ServerHello+Cert+SKE+CR+SHD)
                          gst is configured WITHOUT HelloVerifyRequest, so
                          flight 4 lands directly after flight 1.
38794 > 49444   123 B 255 B 256 B 253 B 255 B 91 B 73 B 25 B
                        ← gst retransmits flight 4 (1s DTLS backoff)
49444 > 38794   149 B   ← pion retransmits ClientHello
...
```

So gst's `ServerHello` (and the rest of flight 4) is on the wire and reaches
pion's socket. `flight1Parse` is supposed to accept a `ServerHello` directly
in flight 1 when the server skipped HelloVerifyRequest (the "HelloVerifyRequest
can be skipped by the server" path that delegates to `flight3Parse`). In
v3.1.3 that path does not fire — pion stays in Flight 1.

## Related stuck point: flight 3 instead of flight 1

When the same pion/dtls v3.1.3 code is exercised against an OpenSSL DTLS
server that *does* use HelloVerifyRequest (production WebRTC ingress
deployment, identical pion + gst-plugins-bad versions), the stall happens
**one flight later** — pion's FSM gets past flight 1 (it processed HVR and
sent the cookied `ClientHello`), then stalls at flight 3 with the same
"keeps retransmitting" symptom while OpenSSL is stuck at
`SSLv3/TLS write server done`.

The two stuck points are observationally distinct but produce the same
end-user symptom (DTLS never completes). The simplest explanation is a single
regression that affects parsing or dispatch of the server's flight 4 records,
manifesting at whichever client flight is current when those records arrive.

## Bisect scope

Tagged version range: `pion/dtls v3.1.2` → `v3.1.3`. Five non-test files
differ between the tags:

| File | Change |
|---|---|
| `handshaker.go` | Retransmit handler: was `if isRetransmit { continue }`, now always calls `parse()`; only the retransmit-timer reset is gated. References issue #758. |
| `handshake_cache.go` | Removed defensive copy in `push()` — cache now stores the caller's slice by reference. |
| `flight3handler.go` | Adds `MasterKeyIdentifier` to `use_srtp` extension in cookied ClientHello. |
| `flight0handler.go`, `flight1handler.go` | Initial `state.namedCurve` changed from hardcoded `defaultNamedCurve` (X25519) to `cfg.ellipticCurves[0]`. |
| `conn.go` | Adds FIPS curve filter (no-op outside FIPS) and a min/max DTLS version helper that, due to a typo, always coerces to DTLS 1.2 (these new fields are stored in `handshakeConfig` but appear unread elsewhere in v3.1.3). |

Top suspects for this symptom are `handshake_cache.go` (cache aliasing could
cause `fullPullMap` to fail to deserialize messages) and `handshaker.go`
(parse() running on retransmit-marked events that previously short-circuited).

## What's in this repo

- `main.go` — pion/webrtc WHIP server, ~100 lines
- `run.sh` — bisect harness (builds and runs both versions in one shot)
- `go.mod`, `go.sum`
