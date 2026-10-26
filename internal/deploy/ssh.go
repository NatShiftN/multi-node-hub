// Package deploy performs the one-time SSH bootstrap used to install an agent.
// It holds no credentials and never logs the SSH password or install command.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Config struct {
	DialTimeout      time.Duration
	HandshakeTimeout time.Duration
	KnownHostsFile   string
	Logger           *slog.Logger
}

type Deployer struct {
	dialTimeout      time.Duration
	handshakeTimeout time.Duration
	hostKey          ssh.HostKeyCallback
}

func New(cfg Config) (*Deployer, error) {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = 15 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	var hostKey ssh.HostKeyCallback
	if cfg.KnownHostsFile != "" {
		callback, err := knownhosts.New(cfg.KnownHostsFile)
		if err != nil {
			return nil, fmt.Errorf("load SSH known_hosts: %w", err)
		}
		hostKey = callback
	} else {
		cfg.Logger.Warn("SSH host key verification is disabled; set --ssh-known-hosts for production")
		hostKey = ssh.InsecureIgnoreHostKey()
	}

	return &Deployer{
		dialTimeout:      cfg.DialTimeout,
		handshakeTimeout: cfg.HandshakeTimeout,
		hostKey:          hostKey,
	}, nil
}

// DeployNode opens a root SSH session, runs installCommand, and closes the
// connection. host may be an IP address or DNS hostname. The caller owns the
// command (and therefore the NodeToken), and must never put the password in it.
// ctx bounds the entire operation.
type Auth struct {
	Password   string
	PrivateKey string
	Passphrase string
}

func (d *Deployer) DeployNode(ctx context.Context, host string, port int, password, installCommand string) error {
	return d.DeployNodeWithAuth(ctx, host, port, Auth{Password: password}, installCommand)
}

func (d *Deployer) DeployNodeWithAuth(ctx context.Context, host string, port int, auth Auth, installCommand string) error {
	if installCommand == "" {
		return errors.New("install command must not be empty")
	}
	var methods []ssh.AuthMethod
	privateKey := strings.TrimSpace(auth.PrivateKey)
	if privateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(privateKey))
		if err != nil {
			var missing *ssh.PassphraseMissingError
			if errors.As(err, &missing) {
				if auth.Passphrase == "" {
					return errors.New("SSH private key is encrypted and requires a passphrase")
				}
				signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(privateKey), []byte(auth.Passphrase))
			}
			if err != nil {
				return fmt.Errorf("parse SSH private key: %w", err)
			}
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if auth.Password != "" {
		methods = append(methods, ssh.Password(auth.Password))
	}
	if len(methods) == 0 {
		return errors.New("SSH password or private key is required")
	}

	address := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{
		Timeout:   d.dialTimeout,
		KeepAlive: 30 * time.Second,
	}
	rawConn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("SSH dial %s: %w", address, err)
	}
	defer rawConn.Close()

	// Closing the socket is the reliable way to interrupt both the handshake
	// and a hanging remote install command when the request context expires.
	stopClose := context.AfterFunc(ctx, func() {
		_ = rawConn.Close()
	})
	defer stopClose()

	_ = rawConn.SetDeadline(time.Now().Add(d.handshakeTimeout))
	clientConfig := &ssh.ClientConfig{
		User:            "root",
		Auth:            methods,
		HostKeyCallback: d.hostKey,
		ClientVersion:   "SSH-2.0-NodeHubDeploy",
		Timeout:         d.dialTimeout,
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(rawConn, address, clientConfig)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("SSH handshake %s: %w", address, err)
	}
	_ = rawConn.SetDeadline(time.Time{})
	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("open SSH session: %w", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput(installCommand)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		message := strings.TrimSpace(string(output))
		if len(message) > 8<<10 {
			message = message[:8<<10] + "\n…(truncated)"
		}
		if message != "" {
			return fmt.Errorf("install command on %s failed: %w: %s", address, err, message)
		}
		return fmt.Errorf("install command on %s failed: %w", address, err)
	}
	return nil
}
