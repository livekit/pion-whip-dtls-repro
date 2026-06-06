#!/usr/bin/env bash
# Reproduce a pion/dtls v3.1.3 regression against gst-plugins-bad whipsink.
#
# Builds the WHIP server twice (pion/dtls v3.1.2 and v3.1.3) and runs a gst
# publisher against each. v3.1.2 reaches "Peer connection state: connected"
# and OnTrack fires; v3.1.3 stalls at "ICE state: connected" indefinitely.

set -euo pipefail
cd "$(dirname "$0")"

# linux/arm64 is what the public livekit/gstreamer image is built for on M-series
# Macs. Change for other hosts: linux/amd64 on Intel/Linux x86_64.
export GOOS=linux
export GOARCH=${GOARCH:-arm64}

GST_IMAGE=livekit/gstreamer:1.24.12-prod-rs
TIMEOUT_SEC=15

for VER in v3.1.2 v3.1.3; do
  echo
  echo "================ pion/dtls $VER ================"
  go get "github.com/pion/dtls/v3@$VER" > /dev/null 2>&1
  go mod tidy > /dev/null 2>&1
  go build -o whip-linux .

  docker run --rm -v "$PWD":/work --entrypoint sh "$GST_IMAGE" -c "
    /work/whip-linux > /tmp/server.log 2>&1 &
    sleep 1
    timeout $TIMEOUT_SEC gst-launch-1.0 \\
      audiotestsrc is-live=true wave=sine-table ! opusenc ! rtpopuspay ! \\
        'application/x-rtp,media=audio,encoding-name=OPUS,payload=96,clock-rate=48000,encoding-params=(string)2' ! whip.sink_0 \\
      videotestsrc is-live=true ! 'video/x-raw,width=640,height=480' ! \\
        x264enc speed-preset=3 tune=zerolatency ! rtph264pay ! \\
        'application/x-rtp,media=video,encoding-name=H264,payload=97,clock-rate=90000' ! whip.sink_1 \\
      whipsink name=whip whip-endpoint=http://127.0.0.1:8080/whip >/dev/null 2>&1 || true
    echo '--- server log ---'
    cat /tmp/server.log
  "
done
