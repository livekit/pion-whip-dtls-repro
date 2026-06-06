// Minimal WHIP server reproducing a pion/dtls v3.1.3 regression.
//
// With pion/dtls v3.1.2 the DTLS handshake against gst-plugins-bad whipsink
// (OpenSSL-backed DTLS server) completes and media starts flowing. With
// pion/dtls v3.1.3 the handshake hangs: ICE reaches "connected" but the
// peer connection never reaches "connected", OnTrack never fires.
//
// Run via:
//   go get github.com/pion/dtls/v3@v3.1.2   # or v3.1.3
//   go build -o whip .
//   ./whip
// and then have a gst whipsink publish to http://<host>:8080/whip.
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/pion/webrtc/v4"
)

func main() {
	http.HandleFunc("/whip", whipHandler)
	log.Println("WHIP server listening on :8080 (POST /whip)")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func whipHandler(res http.ResponseWriter, req *http.Request) {
	offer, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("incoming offer (%d bytes)", len(offer))

	mediaEngine := &webrtc.MediaEngine{}
	mustRegister(mediaEngine, webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeAudio)
	mustRegister(mediaEngine, webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
		},
		PayloadType: 97,
	}, webrtc.RTPCodecTypeVideo)

	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine))
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return
	}

	if _, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio); err != nil {
		log.Fatal(err)
	}
	if _, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo); err != nil {
		log.Fatal(err)
	}

	pc.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		log.Printf("ICE state: %s", s)
	})
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Printf("Peer connection state: %s", s)
	})
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		log.Printf("OnTrack: kind=%s codec=%s", track.Kind(), track.Codec().MimeType)
		buf := make([]byte, 1500)
		for {
			if _, _, err := track.Read(buf); err != nil {
				return
			}
		}
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer, SDP: string(offer),
	}); err != nil {
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return
	}
	if err = pc.SetLocalDescription(answer); err != nil {
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return
	}
	<-gatherComplete

	res.Header().Add("Location", "/whip/x")
	res.Header().Add("Content-Type", "application/sdp")
	res.WriteHeader(http.StatusCreated)
	fmt.Fprint(res, pc.LocalDescription().SDP)
}

func mustRegister(m *webrtc.MediaEngine, p webrtc.RTPCodecParameters, t webrtc.RTPCodecType) {
	if err := m.RegisterCodec(p, t); err != nil {
		log.Fatal(err)
	}
}
