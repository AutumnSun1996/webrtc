//go:build !js
// +build !js

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/ivfreader"
)

var (
	loop     = true
	codecMap = map[string]string{
		"vp8": webrtc.MimeTypeVP8,
		"vp9": webrtc.MimeTypeVP9,
		"av1": webrtc.MimeTypeAV1,
	}
	quickExit = true
)

func ShouldExit(state webrtc.ICEConnectionState) bool {
	if quickExit && state == webrtc.ICEConnectionStateDisconnected {
		return true
	}
	if state == webrtc.ICEConnectionStateFailed || state == webrtc.ICEConnectionStateClosed {
		return true
	}
	return false
}

func createPeerConnection(codec string, offer webrtc.SessionDescription) (answer webrtc.SessionDescription) {
	// Create a MediaEngine object to configure the supported codec
	m := &webrtc.MediaEngine{}

	// Setup the codecs you want to use.
	// We'll use AV1 but you can also define your own
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeAV1, ClockRate: 90000, Channels: 0, SDPFmtpLine: "", RTCPFeedback: nil},
		PayloadType:        41,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		panic(err)
	}

	if err := m.RegisterDefaultCodecs(); err != nil {
		panic(err)
	}

	// Create a InterceptorRegistry. This is the user configurable RTP/RTCP Pipeline.
	// This provides NACKs, RTCP Reports and other features. If you use `webrtc.NewPeerConnection`
	// this is enabled by default. If you are manually managing You MUST create a InterceptorRegistry
	// for each PeerConnection.
	i := &interceptor.Registry{}

	// Use the default set of Interceptors
	if err := webrtc.RegisterDefaultInterceptors(m, i); err != nil {
		panic(err)
	}

	// Create the API object with the MediaEngine
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(i))

	// Create a new RTCPeerConnection
	peerConnection, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		panic(err)
	}
	iceConnectedCtx, iceConnectedCtxCancel := context.WithCancel(context.Background())

	// Create a video track
	videoTrack, videoTrackErr := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: codecMap[codec]}, "video", "pion")
	if videoTrackErr != nil {
		panic(videoTrackErr)
	}

	rtpSender, videoTrackErr := peerConnection.AddTrack(videoTrack)
	if videoTrackErr != nil {
		panic(videoTrackErr)
	}

	// Read incoming RTCP packets
	// Before these packets are returned they are processed by interceptors. For things
	// like NACK this needs to be called.
	go func() {
		rtcpBuf := make([]byte, 1500)
		for {
			if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
				return
			}
			if ShouldExit(peerConnection.ICEConnectionState()) {
				return
			}
		}
	}()

	go func() {
		// Wait for connection established
		<-iceConnectedCtx.Done()

		// Open a IVF file and start reading using our IVFReader
		file, ivfErr := os.Open(codec + ".ivf")
		if ivfErr != nil {
			panic(ivfErr)
		}

		ivf, header, ivfErr := ivfreader.NewWith(file)
		if ivfErr != nil {
			panic(ivfErr)
		}

		idx := 0
		// Send our video file frame at a time. Pace our sending so we send it at the same speed it should be played back as.
		// This isn't required since the video is timestamped, but we will such much higher loss if we send all at once.
		//
		// It is important to use a time.Ticker instead of time.Sleep because
		// * avoids accumulating skew, just calling time.Sleep didn't compensate for the time spent parsing the data
		// * works around latency issues with Sleep (see https://github.com/golang/go/issues/44343)
		frameDuration := time.Millisecond * time.Duration((float32(header.TimebaseNumerator)/float32(header.TimebaseDenominator))*1000)
		ticker := time.NewTicker(frameDuration)
		for ; true; <-ticker.C {
			if ShouldExit(peerConnection.ICEConnectionState()) {
				return
			}
			frame, _, ivfErr := ivf.ParseNextFrame()
			if errors.Is(ivfErr, io.EOF) {
				fmt.Printf("All video frames parsed and sent.\n")
				if loop {
					// If loop is on, seek back to start and send frames again
					file.Seek(0, 0)
					ivf, header, ivfErr = ivfreader.NewWith(file)
					if ivfErr == nil {
						frame, _, ivfErr = ivf.ParseNextFrame()
					}
				} else {
					os.Exit(0)
				}
			}

			if ivfErr != nil {
				panic(ivfErr)
			}

			idx++
			if idx%30 == 0 {
				log.Printf("Send Frame %d\n", idx)
			}
			if ivfErr = videoTrack.WriteSample(media.Sample{Data: frame, Duration: frameDuration}); ivfErr != nil {
				panic(ivfErr)
			}
		}
	}()

	// Set the handler for ICE connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		fmt.Printf("Connection State has changed %s \n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateConnected {
			iceConnectedCtxCancel()
		} else if ShouldExit(peerConnection.ICEConnectionState()) {
			cErr := peerConnection.Close()
			if cErr != nil {
				fmt.Printf("cannot close peerConnection: %v\n", cErr)
			} else {
				fmt.Printf("peerConnection closed\n")
			}
		}
	})

	// Set the handler for Peer connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		fmt.Printf("Peer Connection State has changed: %s\n", s.String())

		if s == webrtc.PeerConnectionStateFailed {
			// Wait until PeerConnection has had no network activity for 30 seconds or another failure. It may be reconnected using an ICE Restart.
			// Use webrtc.PeerConnectionStateDisconnected if you are interested in detecting faster timeout.
			// Note that the PeerConnection may come back from PeerConnectionStateDisconnected.
			fmt.Println("Peer Connection has gone to failed exiting")
			os.Exit(0)
		}
	})

	// Set the remote SessionDescription
	if err = peerConnection.SetRemoteDescription(offer); err != nil {
		panic(err)
	}

	// Create answer
	answer, err = peerConnection.CreateAnswer(nil)
	if err != nil {
		panic(err)
	}
	// fmt.Printf("Answer:\n%s\n", answer.SDP)

	// Create channel that is blocked until ICE Gathering is complete
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	// Sets the LocalDescription, and starts our UDP listeners
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		panic(err)
	}

	// Block until ICE Gathering is complete, disabling trickle ICE
	// we do this because we only can exchange one signaling message
	// in a production application you should exchange ICE Candidates via OnICECandidate
	<-gatherComplete

	// Return the answer
	return *peerConnection.LocalDescription()
}

// Add a single video track
func handleOffer(w http.ResponseWriter, r *http.Request) {
	codec := r.URL.Query().Get("codec")
	if codec == "" {
		codec = "vp8"
	}
	_, ok := codecMap[codec]
	if !ok {
		panic("Invalid codec `" + codec + "`")
	}
	videoFileName := codec + ".ivf"
	// Assert that we have an audio or video file
	_, err := os.Stat(videoFileName)
	haveVideoFile := !os.IsNotExist(err)

	if !haveVideoFile {
		panic("Could not find `" + videoFileName + "`")
	}

	var offer webrtc.SessionDescription
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		panic(err)
	}

	answer := createPeerConnection(codec, offer)

	response, err := json.Marshal(answer)
	if err != nil {
		panic(err)
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(response); err != nil {
		panic(err)
	}
	fmt.Println("PeerConnection has been created")
}

func main() {
	http.Handle("/", http.FileServer(http.Dir(".")))
	http.HandleFunc("/offer", handleOffer)

	go func() {
		fmt.Println("Open http://localhost:8080 to access this demo")
		panic(http.ListenAndServe(":8080", nil))
	}()

	// Block forever
	select {}
}
