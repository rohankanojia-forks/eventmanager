package autoscheduling

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/devtools-qe-incubator/eventmanager/pkg/util/logging"
	"golang.org/x/crypto/ssh"
	"os"
	"strings"
	"time"
)

type BareMetalMachineConfig struct {
	Username       string `json:"username"`
	Host           string `json:"host"`
	PublicKey      string `json:"id_rsa"`
	Command        string `json:"command"`
	ExpectedResult string `json:"expectedResult"`
}

func InspectFreeBareMetalMachines() []string {
	machinesAvailableForUse := make([]string, 0)
	machinesAsCommaSeparatedStr := os.Getenv("BAREMETAL_MACHINES_LIST")
	if machinesAsCommaSeparatedStr != "" {
		machines := strings.Split(machinesAsCommaSeparatedStr, ",")
		for _, machine := range machines {
			logging.Infof("Checking machine %v", machine)
			if isBareMetalMachineAvailable(machine) {
				machinesAvailableForUse = append(machinesAvailableForUse, machine)
			}
		}
	}

	return machinesAvailableForUse
}

func isBareMetalMachineAvailable(machine string) bool {
	bareMetalMachineConfigStr := os.Getenv(machine)
	if bareMetalMachineConfigStr == "" {
		return false
	}

	var config BareMetalMachineConfig
	err := json.Unmarshal([]byte(bareMetalMachineConfigStr), &config)
	if err != nil {
		logging.Errorf("error unmarshaling BareMetalMachineConfig: %v", err)
		return false
	}
	return createSshConnectionAndExecuteCommand(config)
}

func createSshConnectionAndExecuteCommand(config BareMetalMachineConfig) bool {
	key, err := base64.StdEncoding.DecodeString(config.PublicKey)
	if err != nil {
		logging.Info("Failed to read private key:", err)
		return false
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		logging.Info("Failed to parse private key:", err)
		return false
	}

	connectionConfig := &ssh.ClientConfig{
		User: config.Username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	conn, err := ssh.Dial("tcp", fmt.Sprintf("%s:22", config.Host), connectionConfig)
	if err != nil {
		logging.Info("Failed to connect to server:", err)
		return false
	}
	defer conn.Close()

	session, err := conn.NewSession()
	if err != nil {
		logging.Info("Failed to create SSH session:", err)
		return false
	}
	defer session.Close()

	output, err := session.CombinedOutput(config.Command)
	if err != nil {
		logging.Info("Failed to execute command:", err)
		return false
	}

	logging.Info("Command Output:\n", string(output))
	return config.ExpectedResult == string(output)
}
