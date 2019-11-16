package dispatch

import (
	"bqtail/base"
	"bqtail/dispatch/contract"
	"bqtail/service/bq"
	"bqtail/service/secret"
	"bqtail/service/slack"
	"bqtail/service/storage"
	"bqtail/task"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"github.com/viant/afs"
	"github.com/viant/afs/url"
	"google.golang.org/api/bigquery/v2"
	"io"
	"os"
	"sync"
	"time"
)

//Service represents event service
type Service interface {
	Dispatch(ctx context.Context) *contract.Response
	Config() *Config
}

type service struct {
	task.Registry
	lastCheck *time.Time
	config    *Config
	fs        afs.Service
	bq        bq.Service
}

//Config returns service config
func (s *service) Config() *Config {
	return s.config
}

//BQService returns bq service
func (s *service) BQService() bq.Service {
	return s.bq
}

func (s *service) Init(ctx context.Context) error {
	err := s.config.Init(ctx, s.fs)
	if err != nil {
		return err
	}
	slackService := slack.New(s.config.Region, s.config.ProjectID, s.fs, secret.New(), s.config.SlackCredentials)
	slack.InitRegistry(s.Registry, slackService)
	bqService, err := bigquery.NewService(ctx)
	if err != nil {
		return err
	}
	s.bq = bq.New(bqService, s.Registry, s.config.ProjectID, s.fs, s.config.Config)
	bq.InitRegistry(s.Registry, s.bq)
	storage.InitRegistry(s.Registry, storage.New(s.fs))
	return err
}

//Dispatch dispatched BigQuery event
func (s *service) Dispatch(ctx context.Context) *contract.Response {
	response := contract.NewResponse()
	defer response.SetTimeTaken(response.Started)
	timeToLive := s.config.TimeToLive()
	startTime := time.Now()
	for ; ; {
		err := s.dispatch(ctx, response)
		if err != nil {
			response.SetIfError(err)
		}
		response.Cycles++
		if time.Now().Sub(startTime) > timeToLive {
			break
		}
		time.Sleep(time.Second)
	}
	return response
}

func (s *service) processJobs(ctx context.Context, response *contract.Response) error {
	startTime := time.Now()
	modifiedAfter := startTime.Add(- (time.Minute * time.Duration(s.config.MaxJobLoopbackInMin)))
	jobs, err := s.bq.ListJob(ctx, s.config.ProjectID, modifiedAfter, "done")
	if err != nil {
		return err
	}
	response.ListTime = fmt.Sprintf("%s", time.Now().Sub(startTime))
	var jobsByID = make(map[string]*bigquery.JobListJobs)
	for i := range jobs {
		jobsByID[jobs[i].JobReference.JobId] = jobs[i]
	}
	response.JobMatched = len(jobsByID)
	waitGroup := &sync.WaitGroup{}
	err =  s.fs.Walk(ctx, s.Config().DeferTaskURL, func(ctx context.Context, baseURL string, parent string, info os.FileInfo, reader io.Reader) (toContinue bool, err error) {
		if info.IsDir() {
			return true, nil
		}
		URL := url.Join(baseURL, parent, info.Name())
		actions := &task.Actions{}
		if err = json.NewDecoder(reader).Decode(actions); err != nil {
			response.AddError(errors.Wrapf(err, "failed to decode job: %v\n", URL))
			return true, nil
		}
		waitGroup.Add(1)
		go func(URL string, action *task.Actions) {
			defer waitGroup.Done()
			jobID := JobID(s.Config().DeferTaskURL, URL)
			bqJob, ok := jobsByID[jobID]
			if ! ok {
				return
			}
			job, err := s.loadJob(ctx, URL, bqJob, actions)
			if err = s.notify(ctx, job); err != nil {
				response.AddError(err)
			} else {
				response.AddProcessed(jobID)
			}
		}(URL, actions)
		return true, nil
	})
	waitGroup.Wait()
	return err
}


func (s *service) loadJob(ctx context.Context, URL string, job *bigquery.JobListJobs, actions *task.Actions) (*Job, error) {
	//bqJob, err := s.bq.GetJob(ctx, s.config.ProjectID, jobID)
	//if err != nil {
	//	return nil, err
	//}
	//if strings.HasSuffix(jobID, base.DispatchJob) {
	baseJob := base.Job{
		Configuration: job.Configuration,
		Status:        job.Status,
		JobReference:  job.JobReference,
		Statistics:    job.Statistics,
		UserEmail:     job.UserEmail,
	}
	if baseJob.Status == nil {
		baseJob.Status = &bigquery.JobStatus{}
	}
	if baseJob.Status.State == "" {
		baseJob.Status.State = job.State
	}
	if baseJob.Status.ErrorResult == nil {
		baseJob.Status.ErrorResult = job.ErrorResult
	}

	return NewJob(URL, &baseJob, actions), nil
}


func (s *service) dispatch(ctx context.Context, response *contract.Response) (err error) {
	return s.processJobs(ctx, response)
}



func (s *service) notify(ctx context.Context, job *Job) error {
	if exists, _ := s.fs.Exists(ctx, job.URL); !exists {
		return nil
	}
	jobID := job.Id
	if job.Job.JobReference != nil {
		jobID = job.Job.JobReference.JobId
	}
	toRun := job.Actions.ToRun(job.Error(), job.Job, s.config.DeferTaskURL)
	if len(toRun) > 0 {
		actionURL := s.config.BuildActionURL(jobID)
		JSON, err := json.Marshal(toRun)
		if err != nil {
			return err
		}

		if err = s.fs.Upload(ctx, actionURL, 0644, bytes.NewReader(JSON)); err != nil {
			return err
		}
	}
	return s.moveJob(ctx, job)
}

func (s *service) moveJob(ctx context.Context, job *Job) error {
	baseURL := s.config.OutputURL(job.Error() != nil)
	sourceURL := job.URL
	_, sourcePath := url.Base(sourceURL, "")
	if len(s.config.DeferTaskURL) < len(sourceURL) {
		sourcePath = string(sourceURL[len(s.config.DeferTaskURL):])
	}
	destURL := url.Join(baseURL, sourcePath)
	return s.fs.Move(ctx, sourceURL, destURL)
}

//New creates a dispatch service
func New(ctx context.Context, config *Config) (Service, error) {
	srv := &service{
		config:   config,
		fs:       afs.New(),
		Registry: task.NewRegistry(),
	}
	return srv, srv.Init(ctx)
}
