package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/bndr/gojenkins"
	"github.com/joho/godotenv"
	"github.com/peterbourgon/ff/v3"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

func main() {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	var (
		flagJenkinsBase = fs.String("jenkins-base", "https://jenkins.example.net", "jenkins server base")
		flagUsername    = fs.String("jenkins-user", "", "jenkins username")
		flagPassword    = fs.String("jenkins-pass", "", "jenkins password")
		flagJob         = fs.String("jenkins-job", "test-pipeline", "jenkins job")
		flagJobParams   = fs.String("job-params", "jerkins.yaml", "yaml file with jenkins job params")
		flagDebug       = fs.Bool("debug", false, "set logging to debug")
	)
	err := godotenv.Load()
	if err != nil {
		slog.Warn(err.Error())
	}
	if err := ff.Parse(fs, os.Args[1:], ff.WithEnvVars()); err != nil {
		panic(err)
	}
	if *flagDebug {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}
	ctx := context.Background()
	if err := run(ctx, *flagJenkinsBase, *flagUsername, *flagPassword, *flagJob, *flagJobParams); err != nil {
		panic(err)
	}
}

func run(ctx context.Context, base, user, pass, job, jobParamsPath string) error {
	jc := gojenkins.CreateJenkins(nil, base, user, pass)
	if _, err := jc.Init(ctx); err != nil {
		return err
	}
	// load job params
	f, err := os.Open(jobParamsPath)
	if err != nil {
		return err
	}
	defer f.Close()
	params := make(jobParams)
	if err := yaml.NewDecoder(f).Decode(&params); err != nil {
		return err
	}
	if err := params.fillInValues(); err != nil {
		return err
	}
	uncommitedNum, err := getUncommitedChanges()
	if err != nil {
		return err
	}
	if uncommitedNum > 0 {
		branch, _ := getCurrentBranch()
		slog.Warn("you have uncommited changes on your current branch", "branch", branch, "len", uncommitedNum)
	}
	queueId, err := jc.BuildJob(ctx, job, params)
	if err != nil {
		return err
	}
	build, err := jc.GetBuildFromQueueID(ctx, queueId)
	if err != nil {
		return err
	}
	slog.Info("build started", "job", job)
	// Wait for build to finish
	for build.IsRunning(ctx) {
		slog.Debug("still building")
		time.Sleep(3000 * time.Millisecond)
		build.Poll(ctx)
	}
	slog.Info("build done", "result", build.GetResult())
	if build.GetResult() == "FAILURE" {
		slog.Info("logs", "link", fmt.Sprintf("%v/job/%v/%v/console", base, job, build.GetBuildNumber()))
	}
	return nil
}

func getCurrentBranch() (string, error) {
	out, err := script.Exec("git rev-parse --abbrev-ref HEAD").String()
	if err != nil {
		return "", errors.WithMessage(err, out)
	}
	return strings.Trim(out, "\n\t "), nil
}

func getUncommitedChanges() (int, error) {
	return script.Exec("git status --porcelain=v1").CountLines()
}

func getShortHash(branch string) (string, error) {
	out, err := script.Exec("git rev-parse --short --verify " + branch).String()
	if err != nil {
		return "", errors.WithMessage(err, out)
	}
	return strings.Trim(out, "\n\t "), nil
}

type jobParams map[string]string

func (jp jobParams) fillInValues() error {
	branch, err := getCurrentBranch()
	if err != nil {
		return err
	}
	for k, v := range jp {
		if v != "" {
			continue
		}
		switch strings.ToLower(k) {
		case "branch":
			jp[k] = branch
		case "tag":
			tag, err := getShortHash(branch)
			if err != nil {
				return err
			}
			jp[k] = tag
		}
	}
	return nil
}
