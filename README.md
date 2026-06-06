# pion/dtls v3.1.3 regression repro (vs OpenSSL via gst-plugins-bad whipsink)

A WHIP DTLS handshake that succeeds with `github.com/pion/dtls/v3 v3.1.2` hangs
forever with `v3.1.3`. The peer is gst-plugins-bad's `whipsink` element, which
uses OpenSSL's DTLS implementation. ICE reaches `connected` either way; the
DTLS state machine on the pion (DTLS client) side never advances past
flight 3 (cookied ClientHello) in v3.1.3.

## Topology

```
gst-launch (whipsink)               this Go server (pion/webrtc)
  WHIP publisher                      WHIP receiver
  DTLS server (OpenSSL)  ←—DTLS—→    DTLS client (pion/dtls)
```

After SDP/ICE come up, pion-as-DTLS-client sends `ClientHello`, receives
`HelloVerifyRequest`, sends cookied `ClientHello`, and then expects gst's
flight 4 (`ServerHello, Certificate, ServerKeyExchange, CertReq,
ServerHelloDone`). With v3.1.3 the server flight arrives on the wire (visible
in tcpdump) but pion never moves past flight 3 — it keeps retransmitting its
cookied ClientHello until something gives up.

## How to run

Requires: Go, Docker. The Docker image (`livekit/gstreamer:1.24.12-prod-rs`)
ships GStreamer 1.24.12 linked against OpenSSL 3.0.13 and is reachable from
Docker Hub.

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
                                            ← stalled here; no further logs
                                              until gst's 15s timeout
```

## Bisect scope

Tagged version range: pion/dtls v3.1.2 → v3.1.3. Only five non-test files
change between those tags. Most suspicious for this symptom is the
`handshaker.go` retransmit-handling change referencing issue #758
(client now calls `parse()` on `isRetransmit=true` events instead of dropping
them). Other changes:

- `flight3handler.go` adds `MasterKeyIdentifier` to `use_srtp` extension
- `flight0handler.go`, `flight1handler.go` change initial `state.namedCurve`
  preset from `defaultNamedCurve` to `cfg.ellipticCurves[0]`
- `handshake_cache.go` removes the defensive copy of cached bytes
- `conn.go` adds FIPS curve filtering and a min/max DTLS version helper

## What's in this repo

- `main.go` — pion/webrtc WHIP server, ~100 lines
- `run.sh` — bisect harness (builds and runs both versions)
- `go.mod`, `go.sum`
