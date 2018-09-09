package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"strconv"
	"time"

	"github.com/go-redis/redis"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

func (ctx *JobContext) Boot(r *redis.Client) func() {
	port, err := r.Incr("builds.sr.ht.ssh-port").Result()
	if err == nil && port < 22000 {
		port = 22000
		err = r.Set("builds.sr.ht.ssh-port", port, 0).Err()
	} else if err == nil && port >= 23000 {
		port = 22000
		err = r.Set("builds.sr.ht.ssh-port", port, 0).Err()
	}
	if err != nil {
		panic(err)
	}

	ctx.Port = int(port)
	log.Printf("Booting image %s on port %d", ctx.Manifest.Image, port)
	sport := strconv.Itoa(int(port))

	boot := ctx.Control(ctx.Manifest.Image, "boot", sport)
	boot.Stdout = os.Stdout
	boot.Stderr = os.Stderr
	if err := boot.Start(); err != nil {
		panic(err)
	}

	return func() {
		log.Printf("Cleaning up build on port %d", port)
		cleanup := ctx.Control(ctx.Manifest.Image, "cleanup", sport)
		cleanup.Run()
	}
}

func (ctx *JobContext) Settle() error {
	log.Println("Waiting for guest to settle")
	timeout, _ := context.WithTimeout(ctx.Context, 60*time.Second)
	done := make(chan error, 1)
	attempt := 0
	go func() {
		for {
			attempt++
			check := ctx.SSH("echo", "hello world")
			pipe, _ := check.StdoutPipe()
			if err := check.Start(); err != nil {
				done <- err
				return
			}
			stdout, _ := ioutil.ReadAll(pipe)
			if err := check.Wait(); err == nil {
				if string(stdout) == "hello world\n" {
					done <- nil
					return
				} else {
					done <- fmt.Errorf("Unexpected sanity check output: %s",
						string(stdout))
					return
				}
			}

			select {
			case <-timeout.Done():
				done <- fmt.Errorf("Settle timed out after %d attempts",
					attempt)
				return
			case <-time.After(1 * time.Second):
				// Loop
			}
		}
	}()
	return <-done
}

const preamble = `#!/usr/bin/env bash
. ~/.buildenv
set -xe
`

func (ctx *JobContext) SendTasks() error {
	log.Println("Sending tasks")
	const home = "/home/build"
	taskdir := path.Join(home, ".tasks")
	if err := ctx.SSH("mkdir", "-p", taskdir).Run(); err != nil {
		return err
	}
	for _, task := range ctx.Manifest.Tasks {
		var name, script string
		for name, script = range task {
			break
		}
		taskpath := path.Join(taskdir, name)
		if err := ctx.Tee(taskpath, []byte(preamble + script)); err != nil {
			return err
		}
		if err := ctx.SSH("chmod", "755", taskpath).Run(); err != nil {
			return err
		}
	}
	return nil
}

func (ctx *JobContext) SendEnv() error {
	const home = "/home/build"
	log.Println("Sending build environment")
	envpath := path.Join(home, ".buildenv")
	env := `#!/bin/sh
function complete-build() {
	exit 255
}
`
	for key, value := range ctx.Manifest.Environment {
		switch v := value.(type) {
		case string:
			env += fmt.Sprintf("%s=%s\n", key, v)
		case []interface{}:
			env += key + "=("
			for i, _item := range v {
				switch item := _item.(type) {
				case string:
					env += fmt.Sprintf("\"%s\"", item)
				}
				if i != len(v) - 1 {
					env += " "
				}
			}
			env += ")\n"
		default:
			panic(fmt.Errorf("Unknown environment type %T", value))
		}
	}

	if err := ctx.Tee(envpath, []byte(env)); err != nil {
		return err
	}
	if err := ctx.SSH("chmod", "755", envpath).Run(); err != nil {
		return err
	}

	return nil
}

func (ctx *JobContext) SendSecrets() error {
	log.Println("Sending secrets")
	sshKeys := 0
	for _, uuid := range ctx.Manifest.Secrets {
		fmt.Fprintf(ctx.Log, "Resolving secret %s\n", uuid)
		secret, err := GetSecret(ctx.Db, uuid)
		if err != nil {
			return err
		}
		if secret.UserId != ctx.Job.OwnerId {
			fmt.Fprintf(ctx.Log, "Warning: access denied for secret %s\n", uuid)
			continue
		}
		switch secret.SecretType {
		case "ssh_key":
			sshdir := path.Join("/", "home", "build", ".ssh")
			keypath := path.Join(sshdir, uuid)
			if err := ctx.SSH("mkdir", "-p", sshdir).Run(); err != nil {
				return err
			}
			if err := ctx.Tee(keypath, secret.Secret); err != nil {
				return err
			}
			if err := ctx.SSH("chmod", "600", keypath).Run(); err != nil {
				return err
			}
			if sshKeys == 0 {
				if err := ctx.SSH("ln", "-s",
					keypath, path.Join(sshdir, "id_rsa")).Run(); err != nil {

					return err
				}
			}
			sshKeys++
		case "pgp_key":
			gpg := ctx.SSH("gpg", "--import")
			pipe, err := gpg.StdinPipe()
			gpg.Stdout = ctx.Log
			gpg.Stderr = ctx.Log
			if err != nil {
				return err
			}
			if err := gpg.Start(); err != nil {
				return err
			}
			if _, err := pipe.Write(secret.Secret); err != nil {
				return err
			}
			pipe.Close()
			if err := gpg.Wait(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("Unknown secret type %s", secret.SecretType)
		}
	}
	return nil
}

func (ctx *JobContext) RunTasks() error {
	for _, task := range ctx.Manifest.Tasks {
		var (
			err   error
			logfd *os.File
			name  string
			ssh   *exec.Cmd
		)
		for name, _ = range task {
			break
		}

		log.Printf("Running task %s\n", name)
		ctx.Job.SetTaskStatus(name, "running")

		if err = os.Mkdir(path.Join(ctx.LogDir, name), 0755); err != nil {
			goto fail
		}

		ssh = ctx.SSH(path.Join(".", ".tasks", name))
		if logfd, err = os.Create(path.Join(ctx.LogDir, name, "log"));
			err != nil {

			err = errors.Wrap(err, "Creating log file")
			goto fail
		}
		ssh.Stdout = logfd
		ssh.Stderr = logfd

		if err = ssh.Run(); err != nil {
			exiterr, ok := err.(*exec.ExitError)
			if !ok {
				goto fail
			}
			status, ok := exiterr.Sys().(unix.WaitStatus)
			if !ok {
				goto fail
			}
			if status.ExitStatus() == 255 {
				log.Println("TODO: Mark remaining tasks as skipped")
				ctx.Job.SetTaskStatus(name, "success")
				break
			}
			err = errors.Wrap(err, "Running task on guest")
			goto fail
		}

		ctx.Job.SetTaskStatus(name, "success")
		continue
fail:
		ctx.Job.SetTaskStatus(name, "failed")
		return err
	}
	return nil
}
