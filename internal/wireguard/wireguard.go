package wireguard

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os/exec"
	"strings"
	"subscription_bot/internal/models" // импорт структуры WGClient из models
)

func GetClientsFromDocker() ([]models.WGClient, error) {
	cmd := exec.Command("docker", "exec", "wg-easy", "wg", "show", "all", "allowed-ips")
	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return nil, err
	}

	lines := strings.Split(out.String(), "\n")
	clients := []models.WGClient{} // теперь models.WGClient

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		clients = append(clients, models.WGClient{
			PublicKey:  parts[0],
			AllowedIPs: parts[1],
		})
	}

	return clients, nil
}

// Проверяет, что строка состоит из base64-символов
func isBase64(s string) bool {
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') ||
			(c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') ||
			c == '+' || c == '/' || c == '=') {
			return false
		}
	}
	return true
}

// Проверяет и декодирует base64 строку QR-кода в []byte
func DecodeQRCodeBase64(qrBase64 string) ([]byte, error) {
	if !isBase64(qrBase64) {
		return nil, errors.New("строка не является корректным base64")
	}

	data, err := base64.StdEncoding.DecodeString(qrBase64)
	if err != nil {
		return nil, err
	}

	return data, nil
}
