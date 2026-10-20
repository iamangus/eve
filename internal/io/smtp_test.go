package io

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSMTPHonorsCancellationWhileWaitingForGreeting(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			_, _ = io.Copy(io.Discard, conn)
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	number, _ := strconv.Atoi(port)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = (SMTPConfig{Host: host, Port: number, From: "eve@test"}).Send(ctx, "owner@test", "hello", "turn")
	if err == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("SMTP ignored context: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SMTP connection not closed")
	}
}

func TestSMTPAcknowledgementAndStableIdentity(t *testing.T) {
	certificateServer := httptest.NewTLSServer(http.NotFoundHandler())
	defer certificateServer.Close()
	certificate := certificateServer.TLS.Certificates[0]
	pool := x509.NewCertPool()
	pool.AddCert(certificateServer.Certificate())
	for _, ack := range []bool{true, false} {
		t.Run(strconv.FormatBool(ack), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			bodies := make(chan string, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewReader(conn)
				io.WriteString(conn, "220 test SMTP\r\n")
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"):
						io.WriteString(conn, "250-test\r\n250 STARTTLS\r\n")
					case strings.HasPrefix(line, "STARTTLS"):
						io.WriteString(conn, "220 start TLS\r\n")
						secure := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
						if secure.Handshake() != nil {
							return
						}
						conn = secure
						reader = bufio.NewReader(conn)
					case strings.HasPrefix(line, "DATA"):
						io.WriteString(conn, "354 data\r\n")
						var body strings.Builder
						for {
							line, err := reader.ReadString('\n')
							if err != nil {
								return
							}
							if line == ".\r\n" {
								break
							}
							body.WriteString(line)
						}
						bodies <- body.String()
						if ack {
							io.WriteString(conn, "250 accepted\r\n")
						}
						return
					default:
						io.WriteString(conn, "250 ok\r\n")
					}
				}
			}()
			host, port, _ := net.SplitHostPort(listener.Addr().String())
			number, _ := strconv.Atoi(port)
			cfg := SMTPConfig{Host: host, Port: number, From: "eve@test", tlsConfig: &tls.Config{RootCAs: pool, ServerName: "example.com", MinVersion: tls.VersionTLS12}}
			err = cfg.Send(context.Background(), "owner@test", strings.Repeat("full email report\n", 1000), "turn1")
			if ack && err != nil {
				t.Fatalf("QUIT disconnect invalidated successful DATA acknowledgement: %v", err)
			}
			if !ack && !errors.Is(err, ErrSMTPAmbiguous) {
				t.Fatalf("ambiguous delivery not surfaced: %v", err)
			}
			select {
			case body := <-bodies:
				if !strings.Contains(body, "Message-ID: "+stableMessageID("turn1", "owner@test", "eve@test")) || strings.Count(body, "full email report") != 1000 {
					t.Fatal("SMTP identity or full report lost")
				}
			case <-time.After(time.Second):
				t.Fatal("SMTP DATA not received")
			}
		})
	}
}
