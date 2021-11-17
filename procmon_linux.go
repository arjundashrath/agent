//go:build linux
// +build linux

package main

import (
	"fmt"

	"github.com/elastic/go-libaudit/v2"
	"github.com/elastic/go-libaudit/v2/auparse"
	"github.com/elastic/go-libaudit/v2/rule"
	"github.com/elastic/go-libaudit/v2/rule/flags"
	"github.com/pkg/errors"
)

func (p *ProcessMonitor) MonitorProcesses(errc chan error) {
	writeLog("Monitor Processes called")

	client, err := libaudit.NewAuditClient(nil)
	if err != nil {
		errc <- errors.Wrap(err, "failed to new audit client")
	}
	defer client.Close()

	writeLog("NewAuditClient created")

	status, err := client.GetStatus()
	if err != nil {
		errc <- errors.Wrap(err, "failed to get audit client status")
	}

	if status.Enabled == 0 {
		if err = client.SetEnabled(true, libaudit.WaitForReply); err != nil {
			errc <- errors.Wrap(err, "failed to set audit client")
		}
	}

	writeLog("Status is enabled")

	if _, err = client.DeleteRules(); err != nil {
		errc <- errors.Wrap(err, "failed to delete audit rules")
	}

	writeLog("Rules deleted")

	// files modified in working directory
	r, _ := flags.Parse(fmt.Sprintf("-w %s -p wa -k %s", "/home/runner", fileMonitorTag))

	actualBytes, _ := rule.Build(r)

	if err = client.AddRule(actualBytes); err != nil {
		writeLog(fmt.Sprintf("failed to add audit rule %v", err))
		errc <- errors.Wrap(err, "failed to add audit rule")
	}

	writeLog("File monitor added")

	r, _ = flags.Parse(fmt.Sprintf("-w %s -p wa -k %s", "/home/agent", fileMonitorTag))
	actualBytes, _ = rule.Build(r)

	if err = client.AddRule(actualBytes); err != nil {
		writeLog(fmt.Sprintf("failed to add audit rule %v", err))
		errc <- errors.Wrap(err, "failed to add audit rule")
	}

	writeLog("Agent file monitor added")

	// syscall connect
	r, _ = flags.Parse(fmt.Sprintf("-a exit,always -S connect -k %s", netMonitorTag))

	actualBytes, _ = rule.Build(r)

	if err = client.AddRule(actualBytes); err != nil {
		writeLog(fmt.Sprintf("failed to add audit rule %v", err))
		errc <- errors.Wrap(err, "failed to add audit rule for syscall connect")
	}

	writeLog("Net monitor added")

	// syscall process start
	r, _ = flags.Parse(fmt.Sprintf("-a exit,always -S execve -k %s", processMonitorTag))

	actualBytes, _ = rule.Build(r)

	if err = client.AddRule(actualBytes); err != nil {
		writeLog(fmt.Sprintf("failed to add audit rule %v", err))
		errc <- errors.Wrap(err, "failed to add audit rule for syscall execve")
	}

	/*if status.Enabled != 2 {
		writeToFile("setting kernel settings as immutable")
		if err = client.SetImmutable(libaudit.NoWait); err != nil {
			return err
		}
	}*/
	writeLog("Process monitor added")

	// sending message to kernel registering our PID
	if err = client.SetPID(libaudit.NoWait); err != nil {
		errc <- errors.Wrap(err, "failed to set audit PID")
	}

	writeLog("receive called")

	p.receive(client)
}

func (p *ProcessMonitor) receive(r *libaudit.AuditClient) error {

	p.Events = make(map[int]*Event)
	eventHandler := EventHandler{CorrelationId: p.CorrelationId, Repo: p.Repo, ApiClient: p.ApiClient}
	eventHandler.ProcessConnectionMap = make(map[string]bool)
	eventHandler.ProcessFileMap = make(map[string]bool)
	eventHandler.ProcessMap = make(map[string]*Process)

	for {
		rawEvent, err := r.Receive(false)
		if err != nil {
			return errors.Wrap(err, "receive failed")
		}

		// Messages from 1300-2999 are valid audit messages.
		if rawEvent.Type < auparse.AUDIT_USER_AUTH ||
			rawEvent.Type > auparse.AUDIT_LAST_USER_MSG2 {
			continue
		}

		message, err := auparse.Parse(rawEvent.Type, string(rawEvent.Data))
		if err != nil {
			return errors.Wrap(err, "parse failed.")
		}
		eventMap := message.ToMapStr()

		p.PrepareEvent(int(message.Sequence), eventMap)
		if isEventReady(p.Events[int(message.Sequence)]) {
			//writeLog(fmt.Sprintf("event sent %v", p.Events[int(message.Sequence)]))
			go eventHandler.HandleEvent(p.Events[int(message.Sequence)])
		}

	}
}