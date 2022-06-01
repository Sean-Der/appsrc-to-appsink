package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Sean-Der/appsrc-to-appsink/internal/gst"
	janus "github.com/notedit/janus-go"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

func watchHandle(handle *janus.Handle) {
	// wait for event
	for {
		msg := <-handle.Events
		switch msg := msg.(type) {
		case *janus.SlowLinkMsg:
			log.Println("SlowLinkMsg type ", handle.ID)
		case *janus.MediaMsg:
			log.Println("MediaEvent type", msg.Type, " receiving ", msg.Receiving)
		case *janus.WebRTCUpMsg:
			log.Println("WebRTCUp type ", handle.ID)
		case *janus.HangupMsg:
			log.Println("HangupEvent type ", handle.ID)
		case *janus.EventMsg:
			log.Printf("EventMsg %+v", msg.Plugindata.Data)
		}
	}
}

func main() {
	gateway, err := janus.Connect("ws://localhost:8188/janus")
	if err != nil {
		panic(err)
	}

	session, err := gateway.Create()
	if err != nil {
		panic(err)
	}

	handle, err := session.Attach("janus.plugin.videoroom")
	if err != nil {
		panic(err)
	}

	go func() {
		for {
			if _, keepAliveErr := session.KeepAlive(); keepAliveErr != nil {
				panic(keepAliveErr)
			}

			time.Sleep(5 * time.Second)
		}
	}()

	go watchHandle(handle)

	// Create a new RTCPeerConnection
	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{})
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
		fmt.Printf("Connection State has changed %s \n", connectionState.String())
	})

	msg, err := handle.Message(map[string]interface{}{
		"request": "join",
		"ptype":   "subscriber",
		"room":    1234,
		"feed":    1,
	}, nil)
	if err != nil {
		panic(err)
	}

	if msg.Jsep != nil {
		err = peerConnection.SetRemoteDescription(webrtc.SessionDescription{
			Type: webrtc.SDPTypeOffer,
			SDP:  msg.Jsep["sdp"].(string),
		})
		if err != nil {
			panic(err)
		}
	}

	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		panic(err)
	}

	if err := peerConnection.SetLocalDescription(answer); err != nil {
		panic(err)
	}

	msg, err = handle.Message(map[string]interface{}{
		"request": "start",
		"trickle": false,
	}, map[string]interface{}{"type": "answer", "sdp": answer.SDP})
	if err != nil {
		panic(err)
	}

	select {}
}
