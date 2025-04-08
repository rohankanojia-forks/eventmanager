package autoscheduling

import (
	"fmt"
	"github.com/devtools-qe-incubator/eventmanager/pkg/util/logging"
	"golang.org/x/crypto/ssh"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	BareMetalMachineSecretMountPath = "/etc/%s"
)

type BareMetalMachineConfig struct {
	Username  string `json:"username"`
	Host      string `json:"host"`
	PublicKey string `json:"id_rsa"`
	// TODO: Should we also specify what command to run to find whether machine is free
	// and expected output?
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
	config, err := getBareMetalMachineConfigFromMountedSecret(machine)
	if err != nil {
		logging.Errorf("Error getting bare metal machine config from mounted secret for %s: %v", machine, err)
		return false
	}

	return createSshConnectionAndExecuteCommand(config)
}

func getBareMetalMachineConfigFromMountedSecret(machine string) (*BareMetalMachineConfig, error) {
	machineSecretDir := fmt.Sprintf(BareMetalMachineSecretMountPath, machine)
	if _, err := os.Stat(machineSecretDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("baremetal machine secret directory does not exist: %s", machineSecretDir)
	} else if err != nil {
		return nil, fmt.Errorf("error checking baremetal machine secret directory: %w", err)
	}
	logging.Info("Found baremetal machine secret directory")
	config := &BareMetalMachineConfig{}

	dir, err := os.Open(machineSecretDir)
	if err != nil {
		return nil, fmt.Errorf("error opening secret directory: %w", err)
	}
	defer dir.Close()

	fileNames, err := dir.Readdirnames(-1) // -1 reads all entries
	if err != nil {
		return nil, fmt.Errorf("error reading file names from baremetal machine secret directory: %w", err)
	}

	for _, filename := range fileNames {
		filePath := filepath.Join(machineSecretDir, filename)
		info, _ := os.Stat(filePath)
		if !info.IsDir() {
			contentBytes, err := os.ReadFile(filePath)
			if err != nil {
				return nil, fmt.Errorf("error reading file %s: %w", filePath, err)
			}
			content := strings.TrimSuffix(string(contentBytes), "\n")

			switch filename {
			case "user":
				config.Username = content
			case "host":
				config.Host = content
			case "key":
				config.PublicKey = content
				// TODO: Shall we also specify what command to run to find a specific instance is available or not?
			case "command":
				config.Command = content
				// TODO: If we specify what command to run, we should also specify expected output to consider a machine available.
			case "expectedResult":
				config.ExpectedResult = content
			}
		}
	}

	logging.Info("Baremetal machine secret directory found, created obj ", config)
	return config, nil
}

func createSshConnectionAndExecuteCommand(config *BareMetalMachineConfig) bool {
	signer, err := ssh.ParsePrivateKey([]byte(config.PublicKey))
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
