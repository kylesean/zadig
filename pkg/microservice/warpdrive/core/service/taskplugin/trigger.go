/*
Copyright 2021 The KodeRover Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package taskplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configbase "github.com/koderover/zadig/pkg/config"
	"github.com/koderover/zadig/pkg/microservice/warpdrive/config"
	"github.com/koderover/zadig/pkg/microservice/warpdrive/core/service/taskplugin/s3"
	"github.com/koderover/zadig/pkg/microservice/warpdrive/core/service/types/task"
	"github.com/koderover/zadig/pkg/tool/httpclient"
	krkubeclient "github.com/koderover/zadig/pkg/tool/kube/client"
	"github.com/koderover/zadig/pkg/tool/log"
)

const (
	// TriggerTaskTimeout ...
	TriggerTaskTimeout = 60 * 60 * 1 // 60 minutes
)

// InitializeTriggerTaskPlugin to initialize build task plugin, and return reference
func InitializeTriggerTaskPlugin(taskType config.TaskType) TaskPlugin {
	return &TriggerTaskPlugin{
		Name:       taskType,
		kubeClient: krkubeclient.Client(),
	}
}

// TriggerTaskPlugin is Plugin, name should be compatible with task type
type TriggerTaskPlugin struct {
	Name          config.TaskType
	KubeNamespace string
	JobName       string
	FileName      string
	kubeClient    client.Client
	Task          *task.Trigger
	Log           *zap.SugaredLogger
	cancel        context.CancelFunc
	ack           func()
}

func (p *TriggerTaskPlugin) SetAckFunc(ack func()) {
	p.ack = ack
}

// Init ...
func (p *TriggerTaskPlugin) Init(jobname, filename string, xl *zap.SugaredLogger) {
	p.JobName = jobname
	p.Log = xl
	p.FileName = filename
}

func (p *TriggerTaskPlugin) Type() config.TaskType {
	return p.Name
}

// Status ...
func (p *TriggerTaskPlugin) Status() config.Status {
	return p.Task.TaskStatus
}

// SetStatus ...
func (p *TriggerTaskPlugin) SetStatus(status config.Status) {
	p.Task.TaskStatus = status
}

// TaskTimeout ...
func (p *TriggerTaskPlugin) TaskTimeout() int {
	if p.Task.Timeout == 0 {
		p.Task.Timeout = TriggerTaskTimeout
	} else {
		if !p.Task.IsRestart {
			p.Task.Timeout = p.Task.Timeout * 60
		}
	}
	return p.Task.Timeout
}

func (p *TriggerTaskPlugin) SetTriggerStatusCompleted(status config.Status) {
	p.Task.TaskStatus = status
	p.Task.EndTime = time.Now().Unix()
}

func (p *TriggerTaskPlugin) Run(ctx context.Context, pipelineTask *task.Task, pipelineCtx *task.PipelineCtx, serviceName string) {
	var (
		err          error
		body         []byte
		artifactPath string
	)
	defer func() {
		if err != nil {
			p.Log.Error(err)
			p.Task.TaskStatus = config.StatusFailed
			p.Task.Error = err.Error()
			return
		}
	}()
	p.Log.Infof("succeed to create trigger task %s", p.JobName)
	ctx, p.cancel = context.WithCancel(context.Background())
	httpClient := httpclient.New(
		httpclient.SetHostURL(p.Task.URL),
	)
	url := p.Task.Path
	artifactPath, err = p.getS3Storage(pipelineTask)
	if err != nil {
		return
	}
	p.Log.Infof("artifactPath:%s", artifactPath)
	taskOutput := &task.TaskOutput{
		Type:  "object_storage",
		Value: artifactPath,
	}
	webhookPayload := &task.WebhookPayload{
		EventName:   "workflow",
		ProjectName: pipelineTask.ProductName,
		TaskName:    pipelineTask.PipelineName,
		TaskID:      pipelineTask.TaskID,
		TaskOutput:  []*task.TaskOutput{taskOutput},
		TaskEnvs:    pipelineTask.TaskArgs.BuildArgs,
	}
	body, err = json.Marshal(webhookPayload)
	_, err = httpClient.Post(url, httpclient.SetHeader("X-Zadig-Event", "Workflow"), httpclient.SetBody(body))
	if err != nil {
		return
	}
	if !p.Task.IsCallback {
		p.SetTriggerStatusCompleted(config.StatusPassed)
	}
}

func (p *TriggerTaskPlugin) getS3Storage(pipelineTask *task.Task) (string, error) {
	var err error
	var store *s3.S3
	if store, err = s3.NewS3StorageFromEncryptedURI(pipelineTask.StorageURI); err != nil {
		log.Errorf("Archive failed to create s3 storage %s", pipelineTask.StorageURI)
		return "", err
	}
	subPath := ""
	if store.Subfolder != "" {
		subPath = fmt.Sprintf("%s/%s/%s/%s", store.Subfolder, pipelineTask.PipelineName, pipelineTask.ServiceName, "artifact")
	} else {
		subPath = fmt.Sprintf("%s/%s/%s", pipelineTask.PipelineName, pipelineTask.ServiceName, "artifact")
	}
	return fmt.Sprintf("%s/%s/artifact.tar.gz", store.Endpoint, subPath), nil
}

// Wait ...
func (p *TriggerTaskPlugin) Wait(ctx context.Context) {
	timeout := time.After(time.Duration(p.TaskTimeout()) * time.Second)
	defer p.cancel()
	for {
		select {
		case <-ctx.Done():
			p.Task.TaskStatus = config.StatusCancelled
			return
		case <-timeout:
			p.Task.TaskStatus = config.StatusTimeout
			p.Task.Error = "timeout"
			return
		default:
			time.Sleep(time.Second * 3)
			p.Task.CallbackType = "wechat_callback"
			p.Task.CallbackPayload = &task.CallbackPayload{QRCodeURL: "nihao"}
			p.Task.TaskStatus = config.StatusPassed
			return
			//if p.IsTaskDone() {
			//	return
			//}
		}
	}
}

func (p *TriggerTaskPlugin) getCallbackObj(pipelineTask *task.Task) (*task.CallbackPayloadObj, error) {
	url := "/api/"
	httpClient := httpclient.New(
		httpclient.SetHostURL(configbase.AslanServiceAddress()),
	)

	qs := map[string]string{
		"name":        pipelineTask.PipelineName,
		"taskId":      strconv.Itoa(int(pipelineTask.TaskID)),
		"projectName": pipelineTask.ProductName,
	}

	CallbackPayloadObj := new(task.CallbackPayloadObj)
	_, err := httpClient.Get(url, httpclient.SetResult(&CallbackPayloadObj), httpclient.SetQueryParams(qs))
	if err != nil {
		return nil, err
	}
	return CallbackPayloadObj, nil
}

// Complete ...
func (p *TriggerTaskPlugin) Complete(ctx context.Context, pipelineTask *task.Task, serviceName string) {
}

// SetTask ...
func (p *TriggerTaskPlugin) SetTask(t map[string]interface{}) error {
	task, err := ToTriggerTask(t)
	if err != nil {
		return err
	}
	p.Task = task
	return nil
}

// GetTask ...
func (p *TriggerTaskPlugin) GetTask() interface{} {
	return p.Task
}

// IsTaskDone ...
func (p *TriggerTaskPlugin) IsTaskDone() bool {
	if p.Task.TaskStatus != config.StatusCreated && p.Task.TaskStatus != config.StatusRunning {
		return true
	}
	return false
}

// IsTaskFailed ...
func (p *TriggerTaskPlugin) IsTaskFailed() bool {
	if p.Task.TaskStatus == config.StatusFailed || p.Task.TaskStatus == config.StatusTimeout || p.Task.TaskStatus == config.StatusCancelled {
		return true
	}
	return false
}

// SetStartTime ...
func (p *TriggerTaskPlugin) SetStartTime() {
	p.Task.StartTime = time.Now().Unix()
}

// SetEndTime ...
func (p *TriggerTaskPlugin) SetEndTime() {
	p.Task.EndTime = time.Now().Unix()
}

// IsTaskEnabled ...
func (p *TriggerTaskPlugin) IsTaskEnabled() bool {
	return p.Task.Enabled
}

// ResetError ...
func (p *TriggerTaskPlugin) ResetError() {
	p.Task.Error = ""
}
