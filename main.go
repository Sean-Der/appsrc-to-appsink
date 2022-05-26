package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sean-Der/appsrc-to-appsink/internal/gst"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

const indexHTML = `
<!DOCTYPE html>
<html>
  <body>
		<button onclick='start()'>Start</button>

		<h3> ICE Connection States </h3>
		<div id="iceConnectionStates"></div> <br />

		<h1> Local Preview </h1>
		<video id="local-preview" autoplay controls muted> </video>
  </body>

  <script>
  	window.start = () => {
			let pc = new RTCPeerConnection()

    	pc.oniceconnectionstatechange = () => {
    	  let el = document.createElement('p')
    	  el.appendChild(document.createTextNode(pc.iceConnectionState))

    	  document.getElementById('iceConnectionStates').appendChild(el);
    	}

			navigator.mediaDevices.getUserMedia({audio: true, video: true})
			.then(gumStream => {
				document.getElementById('local-preview').srcObject = gumStream

  				for (const track of gumStream.getTracks()) {
  				  pc.addTrack(track)
  				}

				pc.createOffer()
				.then(offer => {
				  pc.setLocalDescription(offer)

				  return fetch('/doSignaling', {
				    method: 'post',
				    headers: {
				  	'Accept': 'application/json, text/plain, */*',
				  	'Content-Type': 'application/json'
				    },
				    body: JSON.stringify(offer)
				  })
				})
				.then(res => res.json())
				.then(res => {
				  pc.setRemoteDescription(res)
				})
				.catch(window.alert)
			})
			.catch(window.alert)
	}
  </script>
</html>
`

var api *webrtc.API //nolint

func doSignaling(w http.ResponseWriter, r *http.Request) {
	peerConnection, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		panic(err)
	}

	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		fmt.Printf("New inbound track %s \n", track.Codec().MimeType)

		// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
		go func() {
			ticker := time.NewTicker(time.Second * 3)
			for range ticker.C {
				rtcpSendErr := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}})
				if rtcpSendErr != nil {
					fmt.Println(rtcpSendErr)
				}
			}
		}()

		codecName := strings.Split(track.Codec().RTPCodecCapability.MimeType, "/")[1]
		fmt.Printf("Track has started, of type %d: %s \n", track.PayloadType(), codecName)
		pipeline := gst.CreatePipeline(track.PayloadType(), strings.ToLower(codecName))
		pipeline.Start()
		buf := make([]byte, 1400)
		for {
			i, _, readErr := track.Read(buf)
			if readErr != nil {
				panic(err)
			}

			pipeline.Push(buf[:i])
		}
	})

	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		fmt.Printf("ICE Connection State has changed: %s\n", connectionState.String())
	})

	var offer webrtc.SessionDescription
	if err = json.NewDecoder(r.Body).Decode(&offer); err != nil {
		panic(err)
	}

	if err = peerConnection.SetRemoteDescription(offer); err != nil {
		panic(err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		panic(err)
	} else if err = peerConnection.SetLocalDescription(answer); err != nil {
		panic(err)
	}
	<-gatherComplete

	response, err := json.Marshal(*peerConnection.LocalDescription())
	if err != nil {
		panic(err)
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(response); err != nil {
		panic(err)
	}
}

func main() {
	go func() {
		m := &webrtc.MediaEngine{}

		if err := m.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, Channels: 0, SDPFmtpLine: "", RTCPFeedback: nil},
			PayloadType:        96,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			panic(err)
		}
		if err := m.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 0, SDPFmtpLine: "", RTCPFeedback: nil},
			PayloadType:        111,
		}, webrtc.RTPCodecTypeAudio); err != nil {
			panic(err)
		}

		i := &interceptor.Registry{}
		if err := webrtc.RegisterDefaultInterceptors(m, i); err != nil {
			panic(err)
		}

		api = webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(i))

		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, indexHTML)
		})
		http.HandleFunc("/doSignaling", doSignaling)

		fmt.Printf("Open http://localhost:8080 to access this demo\n")
		panic(http.ListenAndServe(":8080", nil))
	}()

	gst.StartMainLoop()
}
