package main

import (
	"encoding/binary"
	"fmt"
	"flag"
	"log"
	mrand "math/rand"
	"net"
	"net/http"
	"crypto/rand"
	"strings"
	"sync"
	"time"
)

type RtpMsg struct {
	sn uint16
	delay uint32
}

type RtpStats struct {
	unixtime int64
	recv uint16
	ooo uint16 // Out Of Order packets
	delay uint32
	last_sn uint16
}

var rtpproxyAddr string
var rtpproxyPort uint
var listenPort uint
var payloadSize uint
var payloadType uint
var histSize uint
var histTime uint
var window *MovingWindow
var currRtpStats RtpStats

func init() {
	flag.UintVar(&listenPort, "hport", 8080, "Port to run HTTP server at")
	flag.UintVar(&histSize, "hsize", 10, "History backlog size (in steps)")
	flag.UintVar(&histTime, "htime", 60, "History interval (in seconds)")
	flag.StringVar(&rtpproxyAddr, "rhost", "127.0.0.1", "RTPproxy address")
	flag.UintVar(&rtpproxyPort, "rport", 22222, "RTPproxy port")
	flag.UintVar(&payloadSize, "psize", 160, "RTP payload size (in bytes)")
	flag.UintVar(&payloadType, "ptype", 8, "RTP payload type")
}

func viewHandlerRobo(w http.ResponseWriter, r *http.Request) {
	log.Printf("ROBO FROM: %s REQ: %s\n", r.RemoteAddr, r.URL.Path)
	var retStr string
	retStr += "["

	for i := range window.arr {
		retStr += fmt.Sprintf("{\"unixtime\":%d,\"received\":%d,\"ooo\":%d,\"delay\":%d},",
		window.arr[i].unixtime,
		window.arr[i].recv,
		window.arr[i].ooo,
		window.arr[i].delay)
	}

	retStr += "{}]"
	fmt.Fprintf(w, retStr)
}
func viewHandlerHuman(w http.ResponseWriter, r *http.Request) {
	log.Printf("FROM: %s REQ: %s\n", r.RemoteAddr, r.URL.Path)

	var retStr string

	retStr += "<html lang=\"en\"><head></head><body><table>"

	for i := range window.arr {
		retStr += fmt.Sprintf("<tr><td>%d</td><td>%d</td><td>%d</td><td>%d</td></tr>",
		window.arr[i].unixtime,
		window.arr[i].recv,
		window.arr[i].ooo,
		window.arr[i].delay)
	}

	fmt.Fprintf(w, "<html lang=\"en\"><head></head><body><table>%s</table></head></html>", retStr)
}

// Number of seconds ellapsed from 1900 to 1970, see RFC 5905
const ntpEpochOffset = 2208988800

func getNtpStamp() uint32 {
	tm := time.Now().UnixNano()
	seconds := uint32(tm/1e9 + ntpEpochOffset) // Go uses ns, thus divide by 1e9 to get seconds
	msecs := (seconds % 1e6) * 8000 // 8 KHz
	return msecs + uint32((tm % 1e9) / (1e6 / 8)) // 8 KHz
}

func getRandStr(n uint) string {
	const alphanum = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var bytes = make([]byte, n)
	rand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = alphanum[b % byte(len(alphanum))]
	}
	return string(bytes)
}

func makeCon(name string, haddr string, hport string) *net.UDPConn {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%s", haddr, hport))
	if err != nil {
		log.Printf("ERR1: udp:%s:%s: %v\n", haddr, hport, err)
		return nil
	}

	con, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		log.Printf("ERR2: udp:%s:%s: %v\n", haddr, hport, err)
		return nil
	}
	log.Printf("Connected %s: [udp:%s:%s]\n", name, haddr, hport)
	return con
}

func main() {
	flag.Parse()

	const delay = 20 * time.Millisecond

	var n int

	window = New(int(histSize), 1)

	ca := make(chan RtpMsg)
	cb := make(chan RtpMsg)

	// Buffer for the control connection (~1 MTU)
	var buffer [1500]byte

	const rtpHeader uint = 0x80
	const rtpHeaderSize uint = 12

	var apayload []byte = make([]byte, rtpHeaderSize + payloadSize)

	aSSRC := mrand.Int31()

	apayload[0] = byte(rtpHeader)
	apayload[1] = byte(payloadType)
	binary.BigEndian.PutUint32(apayload[8:], uint32(aSSRC))
	for i := range apayload[rtpHeaderSize:] {
		apayload[rtpHeaderSize + uint(i)] = byte(i % 255)
	}

	var bpayload []byte = make([]byte, rtpHeaderSize + payloadSize)

	bSSRC := mrand.Int31()

	bpayload[0] = byte(rtpHeader)
	bpayload[1] = byte(payloadType)
	binary.BigEndian.PutUint32(bpayload[8:], uint32(bSSRC))
	for i := range bpayload[rtpHeaderSize:]  {
		bpayload[rtpHeaderSize + uint(i)] = byte(255 - i % 255)
	}

	// Synchronizaion object
	var w sync.WaitGroup

	// Wait for the following objects
	// - HTTP server
	// - Alice's sender
	// - Alice's receiver
	// - Bob's sender
	// - Bob's receiver
	// - Stats recalculator
	w.Add(6)

	go func(in1 <-chan RtpMsg, in2 <-chan RtpMsg) {
		var msg RtpMsg
		var tm int64
		for {
			select {
			case msg = <-in1:
				// Alice's receiver
				tm = time.Now().Unix()
				if (tm >= currRtpStats.unixtime + int64(histTime)){
					log.Printf("A RESET: %d: %d samples (%d time): %d %d %d %d %d\n", msg.sn, msg.delay, tm, currRtpStats.unixtime, currRtpStats.recv, currRtpStats.ooo, currRtpStats.delay, currRtpStats.last_sn) // 8 KHz
					// Push it to the Window
					window.PushBack(currRtpStats)
					// ...and clean up
					currRtpStats.unixtime = tm + int64(histTime)
					currRtpStats.recv = 0
					currRtpStats.ooo = 0
					currRtpStats.delay = 0
				}

				//log.Printf("A: %d: %d samples (%d time)\n", msg.sn, msg.delay, tm) // 8 KHz
				// Append stats
				if(msg.sn > currRtpStats.last_sn) {
					currRtpStats.recv++
					currRtpStats.delay += msg.delay
					currRtpStats.ooo += (msg.sn - (currRtpStats.last_sn + 1))
					currRtpStats.last_sn = msg.sn
				}

			case msg = <-in2:
				// Bob's receiver
				//log.Printf("B: %d: %d samples\n", msg.sn, msg.delay) // 8 KHz
			}
		}
		w.Done()
	} (ca, cb)

	// Open control connection
	rtpproxyCon := makeCon("RTPproxy", rtpproxyAddr, fmt.Sprintf("%d", rtpproxyPort))
	// Don't forget to close control connection before exit
	defer rtpproxyCon.Close()

	// Create random Cookie, CallID, FromTag, ToTag
	cookie := getRandStr(8)
	callid := getRandStr(32)
	tagf := getRandStr(16)
	tagt := getRandStr(16)

	// Generate Offer
	offerStr := strings.Join([]string{
		cookie,
		"Uc0,8,18,101",
		callid,
		"192.168.1.100",
		"10560",
		strings.Join([]string{tagf, ";1"},"")}, " ")

	log.Printf("Offer: %s\n", offerStr)
	// Send Offer to RTPproxy
	n, _ = rtpproxyCon.Write([]byte(offerStr))

	// Read Offer reply
	n, _ = rtpproxyCon.Read(buffer[0:])

	// Parse Offer reply
	offerReply := strings.Split(strings.TrimRight(string(buffer[:n]), "\n"), " ")
	log.Printf("Offer: %s [udp:%s:%s]\n", offerReply[0], offerReply[2], offerReply[1])

	// Open Bob's connection
	bobCon := makeCon("Bob", offerReply[2], offerReply[1])
	// Don't forget to close Bob's connection before exit
	defer bobCon.Close()

	// Run Bob's sender
	go func() {
		var sn uint16
		var ts uint32
		sn = 0
		for {
			ts = getNtpStamp()
			binary.BigEndian.PutUint16(bpayload[2:], sn)
			binary.BigEndian.PutUint32(bpayload[4:], ts)
			_, _ = bobCon.Write(bpayload)
			time.Sleep(delay)
			sn++
		}
		w.Done()
	} ()

	// Run Bob's receiver
	go func() {
		var recvbuf []byte = make([]byte, rtpHeaderSize + payloadSize)
		msg := RtpMsg{}
		for {
			_, _ = bobCon.Read(recvbuf[0:])
			msg.sn = binary.BigEndian.Uint16(recvbuf[2:])
			msg.delay = getNtpStamp() - binary.BigEndian.Uint32(recvbuf[4:])
			cb <- msg
		}
		w.Done()
	} ()

	// Generate Answer
	answerStr := strings.Join([]string{
		cookie,
		"Lc0,8,18,101",
		callid,
		"192.168.2.200",
		"20560",
		strings.Join([]string{tagf, ";1"}, ""),
		strings.Join([]string{tagt, ";1"}, "")}, " ")
	log.Printf("Answer: %s\n", answerStr)

	// Send Answer to RTPproxy
	n, _ = rtpproxyCon.Write([]byte(answerStr))

	// Read Answer reply
	n, _ = rtpproxyCon.Read(buffer[0:])

	// Parse Answer reply
	answerReply := strings.Split(strings.TrimRight(string(buffer[:n]), "\n"), " ")
	log.Printf("Answer: %s [udp:%s:%s]\n", answerReply[0], answerReply[2], answerReply[1])

	// Open Alice's connection
	aliceCon := makeCon("Alice", answerReply[2], answerReply[1])
	// Don't forget to close Alice's connection before exit
	defer aliceCon.Close()

	// Run Alice's sender
	go func() {
		var sn uint16
		var ts uint32
		sn = 0
		for {
			ts = getNtpStamp()
			binary.BigEndian.PutUint16(apayload[2:], sn)
			binary.BigEndian.PutUint32(apayload[4:], ts)
			_, _ = aliceCon.Write(apayload)
			time.Sleep(delay)
			sn++
		}
		w.Done()
	} ()

	// Run Alice's  receiver
	go func() {
		var recvbuf []byte = make([]byte, rtpHeaderSize + payloadSize)
		msg := RtpMsg{}
		for {
			_, _ = aliceCon.Read(recvbuf[0:])
			msg.sn = binary.BigEndian.Uint16(recvbuf[2:])
			msg.delay = getNtpStamp() - binary.BigEndian.Uint32(recvbuf[4:])
			ca <- msg
		}
		w.Done()
	} ()

	go func() {
		// Run HTTP stats listener
		http.HandleFunc("/json", viewHandlerRobo)
		http.HandleFunc("/", viewHandlerHuman)
		log.Printf("HTTP started.\n")
		http.ListenAndServe(fmt.Sprintf(":%d", listenPort), nil)
		log.Printf("HTTP stopped.\n")

		w.Done()
	} ()

	w.Wait()

	// Generate Delete
	deleteStr := strings.Join([]string{
		cookie,
		"D",
		callid,
		tagf,
		tagt}, " ")
	log.Printf("Delete: %s\n", deleteStr)

	// Send Delete to RTPproxy
	n, _ = rtpproxyCon.Write([]byte(deleteStr))

	// Read Delete reply
	_, _ = rtpproxyCon.Read(buffer[0:])
}