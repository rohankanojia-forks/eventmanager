package autoscheduling

import (
	tektonClient "github.com/devtools-qe-incubator/eventmanager/pkg/services/cicd/tekton"
	"github.com/devtools-qe-incubator/eventmanager/pkg/util/logging"
	"golang.org/x/exp/slices"
	"os"
	"strconv"
	"time"
)

const (
	DefaultPipelineRunSyncInterval = 5 * time.Second
)

func ManagePipelineRuns(stopChan chan bool) error {
	ticker := time.NewTicker(readPipelineRunSyncInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			availableMachines := InspectFreeBareMetalMachines()
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
