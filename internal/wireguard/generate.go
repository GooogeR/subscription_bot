package wireguard

import (
	"bytes"
	"fmt"
	"os/exec"
)

func AddClientViaDocker(deviceName string) (string, []byte, error) {
	// 1. Добавить клиента
	cmd := exec.Command("docker", "exec", "wg-easy", "add-client", deviceName)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	if err != nil {
		return "", nil, fmt.Errorf("ошибка при создании клиента: %v\n%s", err, out.String())
	}

	// 2. Получить .conf
	confPath := fmt.Sprintf("/etc/wireguard/clients/%s.conf", deviceName)
	catCmd := exec.Command("docker", "exec", "wg-easy", "cat", confPath)
	confOut, err := catCmd.Output()
	if err != nil {
		return "", nil, fmt.Errorf("не удалось получить конфиг: %v", err)
	}

	// 3. Получить QR
	qrCmd := exec.Command("docker", "exec", "wg-easy", "qr", deviceName)
	qrOut, err := qrCmd.Output()
	if err != nil {
		return string(confOut), nil, fmt.Errorf("не удалось получить QR: %v", err)
	}

	return string(confOut), qrOut, nil
}
