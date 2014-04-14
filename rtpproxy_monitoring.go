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

var rtpproxyAddr string
var rtpproxyPort uint
var listenPort uint
var payloadSize uint
var payloadType uint

func init() {
	flag.UintVar(&listenPort, "hport", 8080, "Port to run HTTP server at")
	flag.StringVar(&rtpproxyAddr, "rhost", "127.0.0.1", "RTPproxy address")
	flag.UintVar(&rtpproxyPort, "rport", 22222, "RTPproxy port")
	flag.UintVar(&payloadSize, "psize", 160, "RTP payload size (in bytes)")
	flag.UintVar(&payloadType, "ptype", 8, "RTP payload type")
}

func viewHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("FROM: %s\n", r.RemoteAddr)
	// TODO
	fmt.Fprintf(w, "%s", "<html lang=\"en\"><head></head><body><a href=\"https://www.google.com/\">HELLO</a></body>")
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

	// Wait for 5 objects:
	// - HTTP server
	// - Alice's sender
	// - Alice's receiver
	// - Bob's sender
	// - Bob's receiver
	w.Add(5)

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
		var sn uint16
		var curts uint32
		var origts uint32
		var recvbuf []byte = make([]byte, rtpHeaderSize + payloadSize)
		for {
			_, _ = bobCon.Read(recvbuf[0:])
			curts = getNtpStamp()
			sn = binary.BigEndian.Uint16(recvbuf[2:])
			origts = binary.BigEndian.Uint32(recvbuf[4:])
			// TODO
			log.Printf("B: %d: %d samples\n", sn, curts - origts) // 8 KHz
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
		var sn uint16
		var curts uint32
		var origts uint32
		var recvbuf []byte = make([]byte, rtpHeaderSize + payloadSize)
		for {
			_, _ = aliceCon.Read(recvbuf[0:])
			curts = getNtpStamp()
			sn = binary.BigEndian.Uint16(recvbuf[2:])
			origts = binary.BigEndian.Uint32(recvbuf[4:])
			// TODO
			log.Printf("A: %d: %d samples\n", sn, curts - origts) // 8 KHz
		}
		w.Done()
	} ()

	go func() {
		// Run HTTP stats listener
		http.HandleFunc("/", viewHandler)
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
