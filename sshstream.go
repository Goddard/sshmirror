package main

import (
	"bufio"
	"flag"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

var files []string
var watcher *fsnotify.Watcher

var syncing bool
var masterConnectionAlive bool
var syncingAwait bool

func PanicIf(err error) {
	if err != nil {
		fmt.Println(err.Error())
		panic(err)
	}
}

func runCommand(dir string, cmd string, onStdout func(string), onStderr func(string)) bool {
	command := exec.Command("sh", "-c", cmd)
	command.Dir = dir

	stdout, err := command.StdoutPipe()
	PanicIf(err)
	stdoutScanner := bufio.NewScanner(stdout)
	go func() {
		for stdoutScanner.Scan() {
			stdout := stdoutScanner.Text()
			fmt.Println(stdout)
			if onStdout != nil { onStdout(stdout) }
		}
	}()

	stderr, err := command.StderrPipe()
	PanicIf(err)
	stderrScanner := bufio.NewScanner(stderr)
	go func() {
		for stderrScanner.Scan() {
			stderr := stderrScanner.Text()
			writeToStderr(stderr)
			if onStderr != nil { onStderr(stderr) }
		}
	}()

	return command.Run() == nil
}

func writeToStderr(text string) {
	_, err := fmt.Fprintln(os.Stderr, text)
	PanicIf(err)
}

func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return !os.IsNotExist(err)
}

func syncFiles(localSource string, remoteHost string, remoteDestination string, sshCmd string, verbosity int) {
	if syncingAwait { return }
	syncingAwait = true

	// TODO: something better
	for !masterConnectionAlive { time.Sleep(100 * time.Millisecond) }
	for syncing { time.Sleep(100 * time.Millisecond) }

	syncingAwait = false

	if len(files) == 0 { return }

	syncing = true

	filesUnique := make(map[string]interface{})
	for _, file := range files { filesUnique[file] = nil }
	files = make([]string, 0)

	existing := make([]string, 0)
	deleted := make([]string, 0)
	for file := range filesUnique {
		if fileExists(localSource + "/" + file) {
			existing = append(existing, file)
		} else {
			deleted = append(deleted, file)
		}
	}

	operations := make([]func() bool, 0, 2)
	operations = append(
		operations,
		func() bool {
			if len(existing) == 0 { return true }
			info := fmt.Sprintf("uploading %d file(s)", len(existing))
			if verbosity == 2 { info += fmt.Sprintf(" (%s)", strings.Join(existing, ", ")) }
			return stopwatch(
				info,
				func() bool {
					return runCommand(
						localSource,
						fmt.Sprintf(
							"rsync -azER -e '%s' %s %s:%s > /dev/null",
							sshCmd,
							strings.Join(existing, " "),
							remoteHost,
							remoteDestination,
						),
						nil,
						nil,
					)
				},
			)
		},
	)
	operations = append(
		operations,
		func() bool {
			if len(deleted) == 0 { return true }
			info := fmt.Sprintf("deleting %d file(s)", len(deleted))
			if verbosity == 2 { info += fmt.Sprintf(" (%s)", strings.Join(deleted, ", ")) }
			return stopwatch(
				info,
				func() bool {
					return runCommand(
						localSource,
						fmt.Sprintf(
							"%s %s 'cd %s && rm -rf %s'",
							sshCmd,
							remoteHost,
							remoteDestination,
							strings.Join(deleted, " "),
						),
						nil,
						nil,
					)
				},
			)
		},
	)
	for _, operation := range operations {
		if !operation() {
			files = append(files, existing...)
			files = append(files, deleted...)
			go syncFiles(localSource, remoteHost, remoteDestination, sshCmd, verbosity)
			break
		}
	}

	syncing = false
}

func watchDirRecursive(path string, processor func(fsnotify.Event)) {
	var err error
	watcher, err = fsnotify.NewWatcher()
	PanicIf(err)
	defer func() { PanicIf(watcher.Close()) }()

	PanicIf(filepath.Walk(
		path,
		func(path string, fi os.FileInfo, err error) error {
			PanicIf(err)
			if fi.Mode().IsDir() { return watcher.Add(path) }
			return nil
		},
	))

	done := make(chan bool)

	go func() {
		for {
			select {
				case event := <-watcher.Events: processor(event)
				case err := <-watcher.Errors: PanicIf(err)
			}
		}
	}()

	<-done
}

func stripTrailSlash(path string) string {
	last := len(path) - 1
	if (len(path) > 0) && (path[last:] == "/") { path = path[:last] }
	return path
}

func stopwatch(description string, operation func() bool) bool {
	fmt.Print(description + "... ")
	start := time.Now()
	result := operation()
	if result { fmt.Println("done in " + time.Since(start).String()) }
	return result
}

func main() {
	identityFile := flag.String("i", "", "identity file (rsa)")
	connTimeout  := flag.Int("t", 5, "connection timeout (seconds)")
	exclude      := flag.String("e", "", "exclude pattern (regexp, f.e. '^\\.git/')")
	verbosity    := flag.Int("v", 1, "verbosity level (1-2)")
	flag.Parse()

	var ignored *regexp.Regexp
	if exclude != nil { ignored = regexp.MustCompile(*exclude) }

	localDir   := stripTrailSlash(flag.Arg(0))
	remoteHost := flag.Arg(1)
	remoteDir  := stripTrailSlash(flag.Arg(2))

	if localDir[:2] == "~/" {
		usr, err := user.Current()
		PanicIf(err)
		dir := usr.HomeDir
		localDir = filepath.Join(dir, localDir[2:])
	}

	if flag.NArg() != 3 {
		writeToStderr("Usage: of " + os.Args[0] + ":\nOptional flags:")
		flag.PrintDefaults()
		writeToStderr(
			"Required parameters:\n" +
			"  SOURCE - local directory (absolute path)\n" +
			"  HOST (IP or HOST or USER@HOST)\n" +
			"  DESTINATION - remote directory (absolute path)]",
		)
		return
	}

	sshCmd := fmt.Sprintf(
		"ssh -o ControlMaster=auto -o ControlPath=/tmp/sshstream-%%r@%%h:%%p -o ConnectTimeout=%d -o ConnectionAttempts=1",
		*connTimeout,
	)
	if identityFile != nil { sshCmd += " -i " + *identityFile }

	closeMaster := func() {
		runCommand(
			localDir,
			fmt.Sprintf("%s -O exit %s 2>/dev/null", sshCmd, remoteHost),
			nil,
			nil,
		)
	}

	exit := make(chan os.Signal)
	signal.Notify(exit, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-exit
		closeMaster()
		os.Exit(0)
	}()

	go func() {
		closeMaster()
		for {
			fmt.Print("Establishing SSH Master connection... ")
			runCommand(
				localDir,
				fmt.Sprintf(
					"%s -o ServerAliveInterval=%d -o ServerAliveCountMax=1 -M %s 'echo done && sleep infinity'",
					sshCmd,
					*connTimeout,
					remoteHost,
				),
				func(string) { masterConnectionAlive = true },
				func(string) { masterConnectionAlive = false },
			)
			masterConnectionAlive = false
			time.Sleep(time.Duration(*connTimeout) * time.Second)
		}
	}()

	watchDirRecursive(
		localDir,
		func(event fsnotify.Event) {
			if event.Op == fsnotify.Chmod { return }

			filename := event.Name[len(localDir)+1:]

			if ignored != nil && ignored.MatchString(filename) { return }

			doSync := func() { syncFiles(localDir, remoteHost, remoteDir, sshCmd, *verbosity) }

			if len(files) == 0 {
				go func() {
					time.Sleep(5 * time.Second)
					doSync()
				}()
			}

			go func() {
				time.Sleep(500 * time.Millisecond)
				isLast := (len(files) > 0) && (files[len(files)-1] == filename) // TODO: stop other threads instead
				if isLast { doSync() }
			}()

			files = append(files, filename)
		},
	)
}
