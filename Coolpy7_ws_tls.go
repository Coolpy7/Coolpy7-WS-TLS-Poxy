package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

func main() {
	var (
		debug  = flag.String("debug", "1", "debug 0 is tls mode")
		addr   = flag.String("l", ":8084", "port to listen")
		wsAddr = flag.String("r", "127.0.0.1:8083", "bridge to Coolpy7 WebSocket poxy")
	)

	flag.Parse()

	if conn, err := net.Dial("tcp", *wsAddr); err != nil {
		log.Fatalf("warning: test upstream error: %v", err)
	} else {
		log.Printf("upstream %s ok", *wsAddr)
		conn.Close()
	}

	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		panic(err)
	}
	dir = dir + "/data"
	cert, err := tls.LoadX509KeyPair(dir+"/server.pem", dir+"/server.key")
	if err != nil {
		log.Fatal(err)
	}
	config := &tls.Config{Certificates: []tls.Certificate{cert}}

	srv := http.Server{
		Addr:      *addr,
		Handler:   upstream("tcp", *wsAddr),
		TLSConfig: config,
	}

	go func() {
		if *debug == "1" {
			fmt.Println("is debug mode")
			if err := srv.ListenAndServe(); err != nil {
				log.Fatal(err)
			}
		} else {
			fmt.Println("is product mode")
			if err := srv.ListenAndServeTLS("", ""); err != nil {
				log.Fatal(err)
			}
		}
	}()

	log.Printf("proxy is listening on %q", *addr)
	signalChan := make(chan os.Signal, 1)
	cleanupDone := make(chan bool)
	signal.Notify(signalChan, os.Interrupt)
	go func() {
		for range signalChan {
			ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
			srv.Shutdown(ctx)
			cleanupDone <- true
		}
	}()
	<-cleanupDone
}

func upstream(network, addr string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := net.Dial(network, addr)
		if err != nil {
			log.Printf("dial upstream error: %v", err)
			w.WriteHeader(502)
			return
		}
		if err := r.Write(peer); err != nil {
			log.Printf("write request to upstream error: %v", err)
			w.WriteHeader(502)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(500)
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			w.WriteHeader(500)
			return
		}

		log.Printf(
			"serving %s < %s <~> %s > %s",
			peer.RemoteAddr(), peer.LocalAddr(), conn.RemoteAddr(), conn.LocalAddr(),
		)

		go func() {
			if _, err := io.Copy(peer, conn); err != nil {
				peer.Close()
				conn.Close()
				return
			}
		}()
		go func() {
			if _, err := io.Copy(conn, peer); err != nil {
				peer.Close()
				conn.Close()
				return
			}
		}()
	})
}
