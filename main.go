package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
	"github.com/pion/logging"
	"github.com/pion/turn/v4"
)

type config struct {
	Host   string `env:"HOST"`
	Port   int    `env:"PORT"`
	Secret string `env:"SECRET"`
	Realm  string `env:"REALM"`
}

func getConfig() (config, error) {
	err := godotenv.Load(".env")
	if errors.Is(err, fs.ErrNotExist) {
		log.Default().Println(".env file not found")
	} else if err != nil {
		log.Fatalf("error loading .env file: %T", err)
	}

	c := config{}

	err = env.Parse(&c)

	return c, err
}

func main() {
	config, _ := getConfig()
	peer := flag.String("peer", "", "peer host:port.")
	flag.Parse()

	host := config.Host
	port := config.Port
	secret := config.Secret
	realm := config.Realm

	turnServerAddrStr := fmt.Sprintf("%s:%d", host, port)

	if len(*peer) != 0 {
		peerFunc(turnServerAddrStr, secret, realm, *peer)
	} else {
		listener(turnServerAddrStr, secret, realm)
	}
}

func peerFunc(turnServerAddrStr, user, realm, peer string) {
	conn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		log.Panicf("Failed to listen: %s", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			log.Panicf("Failed to close connection: %s", closeErr)
		}
	}()

	peerAddr, err := net.ResolveUDPAddr("udp", peer)
	if err != nil {
		log.Panicf("Failed to resolve peer address: %s", err)
	}

	if _, err = conn.WriteTo([]byte("Hello"), peerAddr); err != nil {
		log.Panicf("Failed to write: %s", err)
	}
	for {
		log.Printf("try to send to peer %s", peer)
		if _, err = conn.WriteTo([]byte("hello!"), peerAddr); err != nil {
			log.Panicf("Failed to write: %s", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func listener(turnServerAddrStr, user, realm string) {
	conn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		log.Panicf("Failed to listen: %s", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			log.Panicf("Failed to close connection: %s", closeErr)
		}
	}()

	cred := strings.SplitN(user, "=", 2)

	// Start a new TURN Client and wrap our net.Conn in a STUNConn
	// This allows us to simulate datagram based communication over a net.Conn
	cfg := &turn.ClientConfig{
		STUNServerAddr: turnServerAddrStr,
		TURNServerAddr: turnServerAddrStr,
		Conn:           conn,
		Username:       cred[0],
		Password:       cred[1],
		Realm:          realm,
		LoggerFactory:  logging.NewDefaultLoggerFactory(),
	}

	client, err := turn.NewClient(cfg)
	if err != nil {
		log.Panicf("Failed to create TURN client: %s", err)
	}
	defer client.Close()

	// Start listening on the conn provided.
	err = client.Listen()
	if err != nil {
		log.Panicf("Failed to listen: %s", err)
	}

	// Allocate a relay socket on the TURN server. On success, it
	// will return a net.PacketConn which represents the remote
	// socket.
	turnConn, err := client.Allocate()
	if err != nil {
		log.Panicf("Failed to allocate: %s", err)
	}

	log.Printf("relayed-address=%s", turnConn.LocalAddr())

	// Send BindingRequest to learn our external IP
	mappedAddr, err := client.SendBindingRequest()
	if err != nil {
		log.Panicf("Failed to SendBindingRequest: %s", err)
	}

	log.Printf("SendBindingRequest=%s", mappedAddr)

	// Punch a UDP hole for the relayConn by sending a data to the mappedAddr.
	// This will trigger a TURN client to generate a permission request to the
	// TURN server. After this, packets from the IP address will be accepted by
	// the TURN server.
	_, err = turnConn.WriteTo([]byte("Hello"), mappedAddr)
	if err != nil {
		log.Panicf("Failed to write: %s", err)
	}

	defer func() {
		if closeErr := turnConn.Close(); closeErr != nil {
			log.Panicf("Failed to close connection: %s", closeErr)
		}
	}()
	go func() {
		buf := make([]byte, 1600)
		for {
			n, from, readerErr := turnConn.ReadFrom(buf)
			if readerErr != nil {
				break
			}

			msg := string(buf[:n])
			log.Printf("Received from %v: %v", from, msg)
			time.Sleep(100 * time.Millisecond)
		}
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	log.Println("exit")

}
