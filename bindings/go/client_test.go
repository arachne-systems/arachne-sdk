package arachne

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenWorkspaceAndClose(t *testing.T) {
	client, err := Open(Direct, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	state, err := client.WorkspaceState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != PhaseEmpty {
		t.Fatalf("phase = %q, want %q", state.Phase, PhaseEmpty)
	}
	workspaceName := "Go SDK"
	workspace, err := client.CreateWorkspace("Go smoke", &workspaceName)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.UseServiceProfile(); err != nil {
		t.Fatal(err)
	}
	roster, err := client.MemberRoster()
	if err != nil {
		t.Fatal(err)
	}
	if len(roster.Members) != 1 || roster.Members[0].Kind != MemberService {
		t.Fatalf("service profile roster = %+v", roster.Members)
	}
	root := [32]byte{10}
	path := filepath.Join(t.TempDir(), "records.db")
	if err := client.EnableRecordStorage(path, root); err != nil {
		t.Fatal(err)
	}
	if err := client.InstallWorkspacePolicy(workspace.Epoch + 1); err != nil {
		t.Fatal(err)
	}
	var id RecordID
	copy(id[:], bytes.Repeat([]byte{1}, len(id)))
	candidate, err := client.StageProtectedPublication(workspace.Workspace, workspace.Epoch+1,
		"sdk/go/smoke", id, []byte("go binding"))
	if err != nil {
		t.Fatal(err)
	}
	if len(candidate.Snapshot) == 0 {
		t.Fatal("protected publication returned no snapshot")
	}
	if err := client.SaveCandidate(candidate.Snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := client.AdoptProtectedPublication(candidate.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err := client.EnableObjectDelivery(); err != nil {
		t.Fatal(err)
	}
	if err := client.EnableObjectDelivery(); err != nil {
		t.Fatal(err)
	}
	current := PublicationCurrent{Selector: ID{7}, ReplacementKey: ID{8}, ExpiresAt: ^uint64(0)}
	currentCandidate, err := client.StageProtectedPublicationWithCurrent(
		workspace.Workspace, workspace.Epoch+1, "sdk/go/current", RecordID{2}, []byte("current Go value"), &current,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SaveCandidate(currentCandidate.Snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := client.AdoptProtectedPublication(currentCandidate.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.WorkspaceState(); err == nil {
		t.Fatal("Call after Close succeeded")
	}

	restored, err := Open(Direct, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.RestoreRecordStorage(path, root, workspace.Workspace); err != nil {
		t.Fatal(err)
	}
	state, err = restored.WorkspaceState()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Durable || state.Workspace == nil || *state.Workspace != workspace.Workspace {
		t.Fatalf("restored state = %+v", state)
	}
}

func TestProtectedReceive(t *testing.T) {
	owner, err := Open(Direct, bytes.Repeat([]byte{0x31}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	receiver, err := Open(Direct, bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()

	workspace, err := owner.CreateWorkspace("Owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := owner.IssueInvitation()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.InspectInvitation(invitation.Invitation, invitation.Checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := receiver.AddAddressHint(invitation.Peer, loopback(invitation.Address)); err != nil {
		t.Fatal(err)
	}
	join, err := receiver.BeginJoin(invitation.Invitation, invitation.Checkpoint, "Receiver", nil)
	if err != nil {
		t.Fatal(err)
	}
	admissionCandidate, err := owner.StageAdmission(join.Endpoint, join.AdmissionRequest)
	if err != nil {
		t.Fatal(err)
	}
	admittedOwner, err := owner.AdoptAdmission(admissionCandidate.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := owner.RetainedAdmission(join.Endpoint, join.AdmissionRequest)
	if err != nil {
		t.Fatal(err)
	}
	joinCandidate, err := receiver.StageJoin(reply.Welcome, []JoinAdmissionStep{{Commit: reply.Commit, Authorization: reply.Authorization}})
	if err != nil {
		t.Fatal(err)
	}
	admittedReceiver, err := receiver.AdoptJoin(joinCandidate.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if admittedOwner.Epoch != admittedReceiver.Epoch {
		t.Fatalf("admission epochs differ: owner=%d receiver=%d", admittedOwner.Epoch, admittedReceiver.Epoch)
	}
	roster, err := owner.MemberRoster()
	if err != nil {
		t.Fatal(err)
	}
	if len(roster.Members) != 2 {
		t.Fatalf("member roster has %d members, want 2", len(roster.Members))
	}
	metrics, err := owner.Metrics()
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Workspace != workspace.Workspace {
		t.Fatalf("metrics workspace = %v", metrics.Workspace)
	}
	connectivity, err := receiver.Connectivity()
	if err != nil {
		t.Fatal(err)
	}
	if connectivity.Workspace != workspace.Workspace {
		t.Fatalf("connectivity workspace = %v", connectivity.Workspace)
	}

	endpoint, err := receiver.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.AddAddressHint(endpoint.EndpointKey, loopback(endpoint.BoundAddress)); err != nil {
		t.Fatal(err)
	}
	revision := admittedOwner.Epoch + 1
	topic := "sdk/go/receive"
	if err := owner.InstallWorkspacePolicy(revision); err != nil {
		t.Fatal(err)
	}
	if err := receiver.InstallWorkspacePolicy(revision); err != nil {
		t.Fatal(err)
	}
	if err := receiver.SetInterest(workspace.Workspace, revision, topic, true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		observation, err := receiver.PollInterest()
		if err != nil {
			t.Fatal(err)
		}
		if observation != nil {
			if len(observation.Admission.Failed) != 0 {
				t.Fatalf("subscription failed: %+v", observation.Admission.Failed)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("subscription did not settle before the deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}

	payload := []byte("protected Go receive")
	var recordID RecordID
	copy(recordID[:], bytes.Repeat([]byte{1}, len(recordID)))
	publicationCandidate, err := owner.StageProtectedPublication(workspace.Workspace, revision, topic, recordID, payload)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := owner.AdoptProtectedPublication(publicationCandidate.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(delivery.Failed) != 0 {
		t.Fatalf("protected send failed: %+v", delivery.Failed)
	}
	deadline = time.Now().Add(10 * time.Second)
	var reception *ProtectedReceptionCandidate
	for {
		reception, err = receiver.PollProtected()
		if err != nil {
			t.Fatal(err)
		}
		if reception != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("protected publication did not arrive before the deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}
	message, err := receiver.AdoptProtectedReception(reception.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if message.Workspace != workspace.Workspace || message.Topic != topic || !bytes.Equal(message.Payload, payload) {
		t.Fatalf("received publication = %+v", message)
	}
	ownerEndpoint, err := owner.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	if message.Endpoint != ownerEndpoint.EndpointKey {
		t.Fatal("received publication has the wrong authenticated endpoint")
	}
	if _, err := receiver.PollRecoveredPublication(); err != nil {
		t.Fatal(err)
	}

	if _, err := owner.Publish(workspace.Workspace, revision, topic, []byte("basic Go pubsub")); err == nil || !strings.Contains(err.Error(), "unprotected publication is disabled") {
		t.Fatalf("publish error = %v, want admitted-workspace guard", err)
	}
	if _, err := receiver.Poll(); err == nil || !strings.Contains(err.Error(), "use poll_protected") {
		t.Fatalf("poll error = %v, want admitted-workspace guard", err)
	}
}

func loopback(address string) string {
	return strings.Replace(address, "0.0.0.0:", "127.0.0.1:", 1)
}

func TestByteParamsUseJSONArrays(t *testing.T) {
	request, err := encodeRequest("example", map[string]any{"payload": []byte{1, 2, 255}})
	if err != nil {
		t.Fatal(err)
	}
	if string(request) != `{"op":"example","payload":[1,2,255]}` {
		t.Fatalf("request = %s", request)
	}

	request, err = encodeRequest("example", map[string]any{"commits": []JoinAdmissionStep{{
		Commit: Bytes{1, 2},
		Authorization: AdmissionAuthorization{
			GrantSignature:      Bytes{3},
			RedemptionSignature: Bytes{4},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatal(err)
	}
	commits, ok := decoded["commits"].([]any)
	if !ok || len(commits) != 1 {
		t.Fatalf("commits were not encoded as an array: %s", request)
	}
	step, ok := commits[0].(map[string]any)
	if !ok {
		t.Fatalf("commit step was not encoded as an object: %s", request)
	}
	if got, ok := step["commit"].([]any); !ok || len(got) != 2 || got[0] != float64(1) || got[1] != float64(2) {
		t.Fatalf("commit was not encoded as a byte array: %s", request)
	}
	authorization := step["authorization"].(map[string]any)
	if got, ok := authorization["grant_signature"].([]any); !ok || len(got) != 1 || got[0] != float64(3) {
		t.Fatalf("grant signature was not encoded as a byte array: %s", request)
	}
}
