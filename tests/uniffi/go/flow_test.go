// Two-client flow over localhost for the generated Go binding.
package smoke_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	sdk "github.com/arachne-systems/arachne-sdk/generated/go/arachne_sdk"
)

const flowTopic = "streams/uniffi"

func until[T any](t *testing.T, what string, step func() (*T, error)) *T {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		value, err := step()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if value != nil {
			return value
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s timed out", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func local(address string) string { return strings.ReplaceAll(address, "0.0.0.0:", "127.0.0.1:") }

// must panics on an error, which fails the test with a stack trace. Go
// cannot pass a (value, error) pair next to another argument.
func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func noErr(err error) {
	if err != nil {
		panic(err)
	}
}

func openClient(t *testing.T, seed byte) *sdk.Client {
	secret := bytes.Repeat([]byte{seed}, 32)
	return must(sdk.ClientOpen(sdk.ClientConfig{Network: sdk.NetworkDirect, Secret: &secret}))
}

func TestFlow(t *testing.T) {
	owner := openClient(t, 0x91)
	defer owner.Destroy()
	reader := openClient(t, 0x92)
	defer reader.Destroy()
	name := "Go flow"
	created := must(owner.CreateWorkspace("Owner", &name))

	// Invitations (and candidate misuse).
	staged := must(owner.StageInvitation(0, sdk.InvitationKindReusable))
	other := openClient(t, 0x93)
	_, err := other.AdoptInvitation(staged)
	var apiErr *sdk.ApiError
	if !errors.As(err, &apiErr) || sdk.ApiErrorCode(apiErr) != sdk.ErrorCodeWrongState {
		t.Fatalf("adopt on another client: %v", err)
	}
	t.Logf("ok: candidate bound to its client: code %d", sdk.ApiErrorCode(apiErr).Number())
	other.Close()
	other.Destroy()
	invitation := must(owner.AdoptInvitation(staged))
	if _, err := owner.AdoptInvitation(staged); !errors.As(err, &apiErr) || sdk.ApiErrorCode(apiErr) != sdk.ErrorCodeWrongState {
		t.Fatalf("second adopt: %v", err)
	}
	t.Logf("ok: candidate single use")
	details := must(reader.InspectInvitation(invitation.Invitation, invitation.Checkpoint))
	if details.Workspace != created.Workspace {
		t.Fatalf("inspect workspace %s", details.Workspace)
	}

	// Join and admission.
	noErr(reader.AddAddressHint(invitation.Peer, local(invitation.Address)))
	join := must(reader.BeginJoin(invitation.Invitation, invitation.Checkpoint, "Reader", nil))
	ownerView := must(owner.AdoptAdmission(must(owner.StageAdmission(join.Endpoint, join.AdmissionRequest))))
	reply := must(owner.RetainedAdmission(join.Endpoint, join.AdmissionRequest))
	readerView := must(reader.AdoptJoin(must(reader.StageJoin(reply.Welcome,
		[]sdk.JoinAdmissionStep{{Commit: reply.Commit, Authorization: reply.Authorization}}))))
	if ownerView.Epoch != readerView.Epoch || readerView.MemberCount != 2 {
		t.Fatalf("join: %+v %+v", ownerView, readerView)
	}
	t.Logf("ok: joined at epoch %d with %d members", readerView.Epoch, readerView.MemberCount)
	me := must(reader.Describe())
	noErr(owner.AddAddressHint(me.EndpointId, local(me.BoundAddress)))

	// Publication the reader is not subscribed to, then recovery.
	revision := ownerView.Epoch + 1
	noErr(owner.InstallWorkspacePolicy(revision))
	noErr(reader.InstallWorkspacePolicy(revision))
	must(owner.AdoptProtectedPublication(must(owner.StageProtectedPublication(
		created.Workspace, revision, flowTopic, strings.Repeat("01", 16), []byte("first"), nil))))
	var author sdk.MemberId
	for _, m := range must(owner.MemberRoster()).Members {
		if m.SelfMember {
			author = m.Id
		}
	}
	ownerEndpoint := must(owner.Describe()).EndpointId
	after, through := uint64(0), uint64(1)
	must(reader.FetchRecoveryRange(sdk.RecoveryRangeRequest{
		Peer: &ownerEndpoint, Author: &author, Revision: revision,
		Topics: []string{flowTopic}, After: &after, Through: &through,
	}, nil))
	ready := until(t, "recovery range", func() (*sdk.RecoveryRangeStatus, error) {
		if _, err := owner.PollControl(); err != nil {
			return nil, err
		}
		return reader.PollRecoveryRange()
	})
	r, ok := (*ready).(sdk.RecoveryRangeStatusReady)
	if !ok || r.Range.PacketCount != 1 {
		t.Fatalf("range: %#v", *ready)
	}
	t.Logf("ok: recovery range ready: %d packet(s)", r.Range.PacketCount)
	stage, ok := must(reader.StageRecoveryRange(0)).(sdk.RecoveryStageCandidate)
	if !ok {
		t.Fatal("no recovery candidate")
	}
	adoption := must(reader.AdoptRecovery(stage.Candidate))
	if adoption.RecoveredPublications != 1 {
		t.Fatalf("adoption %+v", adoption)
	}
	recovered := must(reader.PollPendingObject())
	if recovered == nil || string(recovered.Payload) != "first" {
		t.Fatalf("recovered %+v", recovered)
	}
	noErr(reader.AdoptProtectedReception(must(reader.StageObjectAcknowledgement(*recovered))))
	if p := must(reader.PollPendingObject()); p != nil {
		t.Fatal("acknowledged object still pending")
	}
	t.Logf("ok: recovered, acknowledged, inbox empty")

	// Interest, live protected receive, rejection.
	noErr(reader.SetInterest(created.Workspace, revision, flowTopic, true))
	observed := until(t, "interest", reader.PollInterest)
	if !observed.Subscribed {
		t.Fatal("interest not subscribed")
	}
	report := must(owner.AdoptProtectedPublication(must(owner.StageProtectedPublication(
		created.Workspace, revision, flowTopic, strings.Repeat("02", 16), []byte("second"), nil))))
	if len(report.Failed) != 0 {
		t.Fatalf("send failed: %+v", report.Failed)
	}
	reception := until(t, "protected receive", reader.PollProtected)
	noErr(reader.AdoptProtectedReception(*reception))
	received := must(reader.PollPendingObject())
	if received == nil || string(received.Payload) != "second" || received.Endpoint != ownerEndpoint {
		t.Fatalf("received %+v", received)
	}
	noErr(reader.AdoptProtectedReception(must(reader.StageObjectRejection(*received))))
	if p := must(reader.PollPendingObject()); p != nil {
		t.Fatal("rejected object still pending")
	}
	t.Logf("ok: live receive, rejected, inbox empty")

	// Presence, metrics, deadline, suspend/resume.
	round := must(owner.PollPresence(false))
	if round.ResponseErrors > 1 {
		t.Fatalf("presence %+v", round)
	}
	if must(reader.Metrics()).Workspace != created.Workspace {
		t.Fatal("metrics workspace")
	}
	deadline := uint64(5000)
	reader.SetDeadline(&deadline)
	noErr(sdk.Suspend())
	if !must(sdk.IsSuspended()) {
		t.Fatal("not suspended")
	}
	noErr(sdk.Resume())
	if must(sdk.IsSuspended()) {
		t.Fatal("still suspended")
	}
	noErr(reader.Close())
	noErr(owner.Close())
	t.Logf("GO FLOW PASS")
}
