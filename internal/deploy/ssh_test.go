package deploy

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func startTestSSHServer(t *testing.T, password string) (host string, port int, commands <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}
	config := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, supplied []byte) (*ssh.Permissions, error) {
			if meta.User() == "root" && string(supplied) == password {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	config.AddHostKey(signer)

	commandCh := make(chan string, 1)
	go func() {
		for {
			rawConn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveTestSSHConn(rawConn, config, commandCh)
		}
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split address: %v", err)
	}
	port, err = strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return host, port, commandCh
}

func serveTestSSHConn(rawConn net.Conn, config *ssh.ServerConfig, commands chan<- string) {
	defer rawConn.Close()
	sshConn, chans, reqs, err := ssh.NewServerConn(rawConn, config)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "session required")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer channel.Close()
			for request := range requests {
				if request.Type != "exec" {
					_ = request.Reply(false, nil)
					continue
				}
				var payload struct {
					Command string
				}
				if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
					_ = request.Reply(false, nil)
					return
				}
				commands <- payload.Command
				_ = request.Reply(true, nil)
				_, _ = channel.Write([]byte("OK\n"))
				_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct {
					Status uint32
				}{Status: 0}))
				return
			}
		}()
	}
}

func TestDeployNodeExecutesCommandOverSSH(t *testing.T) {
	const password = "correct-horse-battery-staple"
	host, port, commands := startTestSSHServer(t, password)

	deployer, err := New(Config{Logger: testLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const command = "echo 'nodehub test'"
	if err := deployer.DeployNode(ctx, host, port, password, command); err != nil {
		t.Fatalf("DeployNode: %v", err)
	}
	select {
	case got := <-commands:
		if got != command {
			t.Fatalf("remote command = %q, want %q", got, command)
		}
	case <-time.After(time.Second):
		t.Fatal("SSH server did not receive the command")
	}
}

func TestDeployNodeDoesNotLeakPasswordInAuthError(t *testing.T) {
	host, port, _ := startTestSSHServer(t, "server-password")
	deployer, err := New(Config{Logger: testLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const wrongPassword = "client-wrong-password-123"
	err = deployer.DeployNode(context.Background(), host, port, wrongPassword, "true")
	if err == nil {
		t.Fatal("expected authentication failure")
	}
	if strings.Contains(err.Error(), wrongPassword) {
		t.Fatalf("authentication error leaked password: %v", err)
	}
}

func TestDeployNodeHonorsContextWhileHandshakeHangs(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(2 * time.Second)
	}()

	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	deployer, err := New(Config{
		DialTimeout:      time.Second,
		HandshakeTimeout: 5 * time.Second,
		Logger:           testLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = deployer.DeployNode(ctx, host, port, "password", "true")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("deploy did not honor context timeout: %s", elapsed)
	}
}
