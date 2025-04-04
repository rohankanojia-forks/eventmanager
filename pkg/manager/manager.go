package manager

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/ssh"
	"golang.org/x/exp/slices"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/devtools-qe-incubator/eventmanager/pkg/configuration/flows"
	"github.com/devtools-qe-incubator/eventmanager/pkg/configuration/providers"
	"github.com/devtools-qe-incubator/eventmanager/pkg/manager/flows/actions"
	"github.com/devtools-qe-incubator/eventmanager/pkg/manager/flows/inputs"
	"github.com/devtools-qe-incubator/eventmanager/pkg/manager/status"

	tektonClient "github.com/devtools-qe-incubator/eventmanager/pkg/services/cicd/tekton"
	"github.com/devtools-qe-incubator/eventmanager/pkg/services/messaging/umb"
	"github.com/devtools-qe-incubator/eventmanager/pkg/services/scm/github"
	"github.com/devtools-qe-incubator/eventmanager/pkg/util"
	"github.com/devtools-qe-incubator/eventmanager/pkg/util/file"
	"github.com/devtools-qe-incubator/eventmanager/pkg/util/logging"
)

const (
	DefaultPipelineRunSyncInterval = 5 * time.Second
)

type BareMetalMachineConfig struct {
	Username       string `json:"username"`
	Host           string `json:"host"`
	PublicKey      string `json:"id_rsa"`
	Command        string `json:"command"`
	ExpectedResult string `json:"expectedResult"`
}

func Initialize(providersFilePath string, flowsFilePath []string) {
	providers, flows, err := loadFiles(providersFilePath, flowsFilePath)
	if err != nil {
		logging.Errorf("error loading configuration files: %v", err)
		os.Exit(1)
	}
	if err = initializeClients(*providers); err != nil {
		logging.Error(err)
		os.Exit(1)
	}
	if err := manageFlows(flows); err != nil {
		logging.Error(err)
		os.Exit(1)
	}
	stopChan := make(chan bool)
	if err := managePipelineRuns(stopChan); err != nil {
		logging.Error(err)
		os.Exit(1)
	}
	if err := status.Init(); err != nil {
		logging.Error(err)
		os.Exit(1)
	}
	// Execute until stop signal
	waitForStop()
	stop()
	os.Exit(0)
}

func managePipelineRuns(stopChan chan bool) error {
	ticker := time.NewTicker(readPipelineRunSyncInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			availableMachines := inspectFreeBareMetalMachines()
			err := listAndSchedulePipelineRuns(availableMachines)
			if err != nil {
				logging.Errorf("problem in listing PipelineRuns: %s", err)
			}
			logging.Infof("finished listing PipelineRuns")
		case <-stopChan:
			logging.Info("Received termination signal, shutting down...")
		}
	}
}

func readPipelineRunSyncInterval() time.Duration {
	intervalStr := os.Getenv("PIPELINE_RUN_SYNC_INTERVAL")
	if intervalStr == "" {
		logging.Infof("PIPELINE_RUN_SYNC_INTERVAL not set, using default interval of 30 seconds")
		return DefaultPipelineRunSyncInterval
	}

	interval, err := strconv.Atoi(intervalStr)
	if err != nil || interval <= 0 {
		logging.Infof("Invalid PIPELINE_RUN_SYNC_INTERVAL value, using default interval of 30 seconds")
		return DefaultPipelineRunSyncInterval
	}

	return time.Duration(interval) * time.Second
}

func inspectFreeBareMetalMachines() []string {
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

func listAndSchedulePipelineRuns(availableMachines []string) error {
	pendingPipelineRuns, err := tektonClient.ListPendingPipelineRuns()
	if err != nil {
		return err
	}
	for _, pendingPipelineRun := range pendingPipelineRuns {
		targetMachine := pendingPipelineRun.GetLabels()["targetBareMetalMachine"]
		if slices.Contains(availableMachines, targetMachine) {
			tektonClient.UpdatePipelineRunStatus(pendingPipelineRun)
		}
	}
	return nil
}

func waitForStop() {
	s := make(chan os.Signal, 1)
	signal.Notify(s,
		os.Interrupt,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	<-s
}

func stop() {
	umb.GracefullShutdown()
	logging.Info("Event manager was gracefully stopped. Enjoy your day!")
}

func loadFiles(providersFilePath string, flowsFilePath []string) (*providers.Providers, *[]flows.Flow, error) {
	var structuredProviders *providers.Providers
	var structuredFlows []flows.Flow
	var err error
	if len(providersFilePath) > 0 {
		structuredProviders, err = providers.LoadFile(providersFilePath)
		if err != nil {
			return nil, nil, err
		}
	}
	if len(flowsFilePath) > 0 {
		for _, flowFilePath := range flowsFilePath {
			if len(flowFilePath) > 0 {
				var flow flows.Flow
				if err := file.LoadFileAsStruct(flowFilePath, &flow); err != nil {
					// Should try to keep loading remaining rules if exist
					logging.Errorf("Can not load flows file: %v", err)
					return nil, nil, err
				}
				structuredFlows = append(structuredFlows, flow)
			}
		}
	}
	return structuredProviders, &structuredFlows, nil
}

func initializeClients(info providers.Providers) error {
	var err error
	if !util.IsEmpty(info.Tekton) {
		err = createTektonClient(info.Tekton)
	}
	if err != nil {
		return fmt.Errorf("error creating tekton client: %v", err)
	}
	if !util.IsEmpty(info.UMB) {
		err = createUMBClient(info.UMB)
	}
	if err != nil {
		return fmt.Errorf("error creating UMB client: %v", err)
	}
	if !util.IsEmpty(info.Github) {
		err = createGithubClient(info.Github)
	}
	if err != nil {
		return fmt.Errorf("error creating Github client: %v", err)
	}
	return nil
}

func createTektonClient(info providers.Tekton) (err error) {
	var workspaces []tektonClient.WorkspaceBinding
	if len(info.Workspaces) > 0 {
		for _, item := range info.Workspaces {
			var adaptedItem tektonClient.WorkspaceBinding
			adaptedItem.Name = item.Name
			adaptedItem.PVC = item.PVC
			workspaces = append(workspaces, adaptedItem)
		}
	}
	kubeconfig := []byte("")
	if len(info.Kubeconfig) > 0 {
		kubeconfig, err = base64.StdEncoding.DecodeString(info.Kubeconfig)
		if err != nil {
			return
		}
	}
	return tektonClient.CreateClient(kubeconfig, info.Namespace, workspaces, info.ConsoleURL)
}

func createUMBClient(info providers.UMB) (err error) {
	userCertificate, userKey, certificateAuthority, err := providers.ParseUMBFiles(info)
	if err != nil {
		return err
	}
	err = umb.InitClient(
		info.ConsumerID,
		info.Driver,
		strings.Split(info.Brokers, ","),
		userCertificate,
		userKey,
		certificateAuthority)
	return
}

func createGithubClient(info providers.Github) error {
	if len(info.AppKey) > 0 {
		appKey, err := providers.ParseGithubFiles(info)
		if err != nil {
			return err
		}
		return github.CreateClientForApp(info.AppID, info.AppInstallationID, appKey)
	}
	return github.CreateClientForUser(info.Token)
}

// For each flow defined:
// Create action that action
func manageFlows(flows *[]flows.Flow) error {
	if len(*flows) > 0 {
		for _, flow := range *flows {
			logging.Debugf("Setting up flow: %v", flow)
			action, err := actions.CreateAction(flow)
			if err != nil {
				logging.Errorf("Find error with flow %s:%v", flow.Name, err)
				break
			}
			err = inputs.AddActionToInput(flow, action)
			if err != nil {
				logging.Errorf("Find error with flow %s:%v", flow.Name, err)
				break
			}
		}
	}
	return nil
}
