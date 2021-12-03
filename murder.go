package main

import (
	"fmt"
	"strings"
	"os"
	"regexp"
	"io/ioutil"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	nProcs      int64 = 0
	nRestricted int64 = 0
	nScanned    int64 = 0
	nMatched    int64 = 0
	nKilled     int64 = 0
	nProtected  int64 = 0
)

type Process struct {
	pid     int
	proc    *os.Process
	cmdline []byte
}

func listPIDs(pids chan int) {
	files, err := ioutil.ReadDir("/proc")
	if err != nil {
		panic(fmt.Sprintf("Error reading /proc: %v\n", err))
	}
	for _, f := range files {
		pid, err := strconv.Atoi(f.Name())
		if err == nil {
			nProcs += 1
			pids <- pid
		}
	}
	close(pids)
}

func openProcs(pids chan int, procs chan Process) {
	for pid := range pids {
		proc, err := os.FindProcess(pid)
		if err != nil {
			nRestricted += 1
			continue;
		}
		path := fmt.Sprintf("/proc/%d/cmdline", pid)
		name, err := ioutil.ReadFile(path)
		if err != nil {
			panic(fmt.Sprintf("Error: could not open cmd of pid %d\n", pid))
		}
		procs <- Process{ pid: pid, proc: proc, cmdline: name }
	}
	close(procs)
}

func matchProcs(pattern *regexp.Regexp, inProcs chan Process, outProcs chan Process) {
	localPid := os.Getpid()
	selfPattern := regexp.MustCompile("/usr/local/go/bin/go")
	for proc := range inProcs {
		nScanned += 1
		if pattern.Match(proc.cmdline) && !selfPattern.Match(proc.cmdline) && proc.pid != localPid {
			nMatched += 1
			outProcs <- proc
		}
	}
	close(outProcs)
}

func monitorProc(proc *os.Process) chan *os.ProcessState {
	stateChan := make(chan *os.ProcessState)
	go func(p *os.Process) {
		state, _ := p.Wait()
		stateChan <- nil
		//if err != nil {
		//	fmt.Printf("Error waiting on process: %d %v\n", proc.Pid, err)
		//}
		stateChan <- state
		close(stateChan)
	}(proc)
	return stateChan
}

func exited(state *os.ProcessState) (didExit bool) {
	defer func() {
		if panicInfo := recover(); panicInfo != nil {
			didExit = true
		}
	}()
	if state.Exited() {
		didExit = true
	} else {
		didExit = false
	}
	return didExit
}

// Try to kill
// print about it
// return diagnostics
func tryKill(proc Process) {
	stateChan := monitorProc(proc.proc)
	<-stateChan
	proc.proc.Kill()
	state := <-stateChan
	if exited(state) {
		fmt.Printf("Killed Process %d: '%s'\n", proc.pid, proc.cmdline)
		atomic.AddInt64(&nKilled, 1)
	} else {
		fmt.Printf("Could not kill %d: '%s'\n", proc.pid, proc.cmdline)
		atomic.AddInt64(&nProtected, 1)
	}
}

func main() {
	// Read arguments
	if len(os.Args) == 1 {
		panic(fmt.Sprintf("Error: no arguments provided\n"))
	}
	arg := strings.Join(os.Args[1:], " ")

	// Compile main regex
	pattern, err := regexp.Compile(arg)
	if err != nil {
		panic(fmt.Sprintf("Error: could not compile: '%s': %v\n", pattern, err))
	}

	// Find files
	bufSize := 100
	pids := make(chan int, bufSize)
	procs := make(chan Process, bufSize)
	filteredProcs := make(chan Process, bufSize)

	go listPIDs(pids)
	go openProcs(pids, procs)
	go matchProcs(pattern, procs, filteredProcs)

	var wg sync.WaitGroup

	for proc := range filteredProcs {
		wg.Add(1)
		go func(p Process) {
			tryKill(p)
			wg.Done()
		}(proc)
	}

	wg.Wait()
	fmt.Printf("Stats:\n")
	fmt.Printf("\t%d Processes\n", nProcs)
	fmt.Printf("\t%d Restricted\n", nRestricted)
	fmt.Printf("\t%d Scanned\n", nScanned)
	fmt.Printf("\t%d Matched\n", nMatched)
	fmt.Printf("\t%d Killed\n", nKilled)
	fmt.Printf("\t%d Protected\n", nProtected)
}
