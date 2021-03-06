package provision

import (
	"bytes"
	"fmt"
	"golang.org/x/crypto/ssh"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"time"
)

type SSH struct {
	client  *ssh.Client
	stdout  io.Writer
	stderr  io.Writer
	Error   error
}

func NewSSH(
	ip string,
	port string,
	key []byte,
	timeout time.Duration,
	stdout io.Writer,
	stderr io.Writer,
) (*SSH, error) {
	client, err := waitForSSH(ip, port, key, timeout)
	if err != nil {
		return nil, err
	}

	return &SSH{
		client:  client,
		stdout:  stdout,
		stderr:  stderr,
	}, nil
}

func (s *SSH) Close() {
	s.client.Close()
}

func (s *SSH) Run(command string) {
	if s.Error != nil {
		return
	}

	session, err := s.client.NewSession()
	if err != nil {
		s.Error = err
		return
	}
	defer session.Close()

	session.Stdout = s.stdout
	session.Stderr = s.stderr

	s.Error = session.Run(command)
}

func (s *SSH) SendFile(filePath string, remoteFilePath string) {
	if s.Error != nil {
		return
	}

	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		s.Error = err
		return
	}

	s.SendData(data, remoteFilePath)
}


func (s *SSH) SendData(srcData []byte, remoteFilePath string) {
	if s.Error != nil {
		return
	}

	bytesReader := bytes.NewReader(srcData)
	session, err := s.client.NewSession()
	if err != nil {
		s.Error = err
		return
	}
	defer session.Close()

	session.Stdout = s.stdout
	session.Stderr = s.stderr

	go func() {
		w, _ := session.StdinPipe()
		defer w.Close()

		fmt.Fprintln(w, "C0755", int64(len(srcData)), filepath.Base(remoteFilePath))
		_, err := io.Copy(w, bytesReader)
		if err != nil {
			fmt.Print(err)
		}

		fmt.Fprintln(w, "\x00")
	}()

	command := fmt.Sprintf("/usr/bin/scp -qt %s", filepath.Dir(remoteFilePath))

	// we are ignoring the error here because it happens even during success
	session.Run(command)
}

func (s *SSH) RetrieveFile(filePath string, remoteFilePath string) {
	if s.Error != nil {
		return
	}

	f, err := os.Create(filePath)
	if err != nil {
		s.Error = err
		return
	}
	defer f.Close()

	session, err := s.client.NewSession()
	if err != nil {
		s.Error = err
		return
	}
	defer session.Close()

	session.Stdout = f
	session.Stderr = s.stderr

	s.Error = session.Run("cat " + remoteFilePath)
}

func waitForSSH(ip string, port string, privateKey []byte, timeout time.Duration) (*ssh.Client, error) {
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("could not parse private key: %s", err)
	}

	var (
		clientChan = make(chan *ssh.Client, 1)
		errorChan  = make(chan error, 1)
		config     = &ssh.ClientConfig{
			User:    "root",
			Auth:    []ssh.AuthMethod{ssh.PublicKeys(signer)},
			Timeout: 10 * time.Second,
			HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
				return nil
			},
		}
	)

	go func() {
		var (
			ticker  = time.NewTicker(time.Second)
			timeout = time.After(timeout)
			err     error
		)

		for {
			select {
			case <-ticker.C:
				var client *ssh.Client
				client, err = ssh.Dial("tcp", ip+":"+port, config)
				if err == nil {
					clientChan <- client
					errorChan <- nil
					return
				}
			case <-timeout:
				clientChan <- nil
				errorChan <- fmt.Errorf("ssh connection timed out: %s", err)
				return
			}
		}
	}()

	return <-clientChan, <-errorChan
}
