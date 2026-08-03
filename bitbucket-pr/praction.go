package main

import (
	"fmt"
	"strings"
)

// detailActionKind is the one confirmation slot shared by comment deletion
// and PR-level mutations. Only one action can wait for `y` at a time.
type detailActionKind uint8

const (
	detailActionNone detailActionKind = iota
	detailActionDeleteComment
	detailActionApprove
	detailActionUnapprove
	detailActionDecline
	detailActionClone // Outline tab: clone the repo to enable analysis
)

type pendingDetailAction struct {
	kind      detailActionKind
	commentID int
	prompt    string
}

func (k detailActionKind) name() string {
	switch k {
	case detailActionDeleteComment:
		return "コメント削除"
	case detailActionApprove:
		return "承認"
	case detailActionUnapprove:
		return "承認取消"
	case detailActionDecline:
		return "Decline"
	case detailActionClone:
		return "clone"
	default:
		return "操作"
	}
}

func (v *detailView) beginPRAction(a *app, kind detailActionKind) {
	if v.prActionRunning {
		a.status = "PR操作を実行中です"
		return
	}
	d := a.detailFor(v.prID)
	if d.pr == nil {
		a.status = "PR情報の読み込み完了後に操作してください"
		return
	}
	if strings.ToUpper(d.pr.State) != "OPEN" {
		a.status = "OPENのPRのみ承認・承認取消・Declineできます"
		return
	}
	v.pendingAction = pendingDetailAction{kind: kind}
	switch kind {
	case detailActionApprove:
		v.pendingAction.prompt = "このPRを承認しますか？"
	case detailActionUnapprove:
		v.pendingAction.prompt = "自分の承認を取り消しますか？"
	case detailActionDecline:
		v.pendingAction.prompt = fmt.Sprintf("PR #%d をDeclineしますか？", v.prID)
	}
}

func (v *detailView) modal(a *app) *modalSpec {
	pending := v.pendingAction
	if pending.kind == detailActionNone {
		return nil
	}
	return &modalSpec{
		Title: pending.kind.name(),
		Lines: []modalLine{
			{Spans: []Span{{Text: pending.prompt, Style: styleTitle}}, Center: true},
			{},
		},
		Footer: modalLine{Spans: []Span{
			{Text: "y", Style: styleApproved},
			{Text: " 実行     その他のキー 取消", Style: styleDim},
		}, Center: true},
		PreferredWidth: 54,
	}
}

// handlePendingAction consumes the key after a confirmation prompt. The
// caller handles q/Esc through interceptEsc; every other non-y key cancels.
func (v *detailView) handlePendingAction(a *app, k Key) {
	pending := v.pendingAction
	v.pendingAction = pendingDetailAction{}
	if !isKey(k, 'y') {
		a.status = pending.kind.name() + "を取り消しました"
		return
	}
	switch pending.kind {
	case detailActionDeleteComment:
		id := pending.commentID
		v.runCommentAction(a, "コメントを削除しました", func(cl *bbClient) error {
			return cl.deleteComment(a.ctx.Workspace, a.ctx.Repo, v.prID, id)
		})
	case detailActionApprove, detailActionUnapprove, detailActionDecline:
		v.runPRAction(a, pending.kind)
	case detailActionClone:
		v.runOutlineClone(a)
	}
}

func (v *detailView) cancelPendingAction(a *app) bool {
	if v.pendingAction.kind == detailActionNone {
		return false
	}
	name := v.pendingAction.kind.name()
	v.pendingAction = pendingDetailAction{}
	a.status = name + "を取り消しました"
	return true
}

// runPRAction executes one confirmed mutation off the main loop. Approve and
// Decline return enough data for immediate local updates; Unapprove answers
// 204, so only that path follows with a targeted PR-detail fetch.
func (v *detailView) runPRAction(a *app, kind detailActionKind) {
	if v.prActionRunning {
		a.status = "PR操作を実行中です"
		return
	}
	v.prActionRunning = true
	prID := v.prID
	client := a.client
	ws, repo := a.ctx.Workspace, a.ctx.Repo
	a.fetch(fmt.Sprintf("pr %s %d", strings.ToLower(kind.name()), prID), func() (func(*app), error) {
		var (
			participant *Participant
			pr          *PullRequest
			actionErr   error
			refreshErr  error
		)
		switch kind {
		case detailActionApprove:
			participant, actionErr = client.approvePR(ws, repo, prID)
		case detailActionUnapprove:
			actionErr = client.unapprovePR(ws, repo, prID)
			if actionErr == nil {
				pr, refreshErr = client.getPR(ws, repo, prID)
			}
		case detailActionDecline:
			pr, actionErr = client.declinePR(ws, repo, prID)
		}

		// Return an apply closure even on error so the per-view running flag is
		// always cleared. Mutation failures still surface in the status line.
		return func(a *app) {
			v.prActionRunning = false
			if actionErr != nil {
				a.status = kind.name() + "に失敗: " + actionErr.Error()
				debugf("pr action %s %d: %v", kind.name(), prID, actionErr)
				return
			}
			if refreshErr != nil {
				a.status = kind.name() + "しました（表示更新に失敗。rで再読込してください）: " + refreshErr.Error()
				debugf("pr action %s %d refresh: %v", kind.name(), prID, refreshErr)
				return
			}

			switch kind {
			case detailActionApprove:
				v.applyApproval(a, *participant)
				a.status = "PRを承認しました"
			case detailActionUnapprove:
				v.applyPRSnapshot(a, pr)
				a.status = "自分の承認を取り消しました"
			case detailActionDecline:
				v.applyPRSnapshot(a, pr)
				a.status = "PRをDeclineしました"
			}
		}, nil
	})
}

func upsertParticipant(pr *PullRequest, participant Participant) {
	if pr == nil {
		return
	}
	key := accountKey(participant.User)
	for i := range pr.Participants {
		if key != "" && accountKey(pr.Participants[i].User) == key {
			pr.Participants[i] = participant
			return
		}
	}
	pr.Participants = append(pr.Participants, participant)
}

func (v *detailView) applyApproval(a *app, participant Participant) {
	d := a.detailFor(v.prID)
	upsertParticipant(d.pr, participant)
	if v.summary != nil && v.summary != d.pr {
		upsertParticipant(v.summary, participant)
	}
	for i := range a.prs {
		if a.prs[i].ID == v.prID {
			upsertParticipant(&a.prs[i], participant)
		}
	}
	mutatePRCache(a.ctx.Workspace, a.ctx.Repo, "OPEN", v.prID, func(pr *PullRequest) bool {
		upsertParticipant(pr, participant)
		return true
	})
	v.rebuild(a)
	rebuildListViews(a)
}

func (v *detailView) applyPRSnapshot(a *app, pr *PullRequest) {
	if pr == nil {
		return
	}
	a.detailFor(v.prID).pr = pr
	v.summary = pr
	for i := range a.prs {
		if a.prs[i].ID != v.prID {
			continue
		}
		if strings.EqualFold(pr.State, a.prsState) {
			a.prs[i] = *pr
		} else {
			a.prs = append(a.prs[:i], a.prs[i+1:]...)
		}
		break
	}
	mutatePRCache(a.ctx.Workspace, a.ctx.Repo, "OPEN", v.prID, func(cached *PullRequest) bool {
		if !strings.EqualFold(pr.State, "OPEN") {
			return false
		}
		*cached = *pr
		return true
	})
	v.rebuild(a)
	rebuildListViews(a)
}

func rebuildListViews(a *app) {
	for _, screen := range a.stack {
		if list, ok := screen.(*listView); ok {
			list.rebuild(a)
		}
	}
}
