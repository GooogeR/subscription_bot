package sshclient

import (
	"fmt"
	"io/ioutil"

	"golang.org/x/crypto/ssh"
)

func runSSHCommand(cmd string) (string, error) {
	user := "root"
	server := "89.22.233.83:22"
	keyPath := "/Users/goooger/.ssh/id_rsa"

	key, err := ioutil.ReadFile(keyPath)
	if err != nil {
		return "", fmt.Errorf("не удалось прочитать SSH ключ: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return "", fmt.Errorf("не удалось распарсить ключ: %w", err)
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", server, config)
	if err != nil {
		return "", fmt.Errorf("не удалось подключиться к серверу: %w", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("не удалось создать сессию: %w", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput(cmd)
	if err != nil {
		return "", fmt.Errorf("ошибка выполнения команды: %w", err)
	}

	return string(output), nil
}

func GetWGConfigFromDocker() (string, error) {
	cmd := "docker exec wg-easy cat /etc/wireguard/wg0.conf"
	return runSSHCommand(cmd)
}
