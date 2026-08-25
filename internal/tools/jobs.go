package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// JobStatusInput selects a namespace.
type JobStatusInput struct {
	Namespace string `json:"namespace" jsonschema:"namespace whose Jobs and CronJobs to list"`
}

// JobStatus implements the job_status tool.
func (t *Toolset) JobStatus(ctx context.Context, req *mcp.CallToolRequest, in JobStatusInput) (*mcp.CallToolResult, JobStatusOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	if err := t.checkNamespace(in.Namespace); err != nil {
		return nil, JobStatusOutput{}, err
	}

	out := JobStatusOutput{Namespace: in.Namespace, Jobs: []JobInfo{}, CronJobs: []CronJobInfo{}}

	jobs, err := t.clients.Typed.BatchV1().Jobs(in.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, out, err
	}
	for i := range jobs.Items {
		job := &jobs.Items[i]
		info := JobInfo{
			Name:           job.Name,
			Active:         job.Status.Active,
			Succeeded:      job.Status.Succeeded,
			Failed:         job.Status.Failed,
			StartTime:      fmtTimePtr(job.Status.StartTime),
			CompletionTime: fmtTimePtr(job.Status.CompletionTime),
		}
		for _, owner := range job.OwnerReferences {
			if owner.Kind == "CronJob" {
				info.OwnedBy = "CronJob/" + owner.Name
			}
		}
		for _, c := range job.Status.Conditions {
			info.Conditions = append(info.Conditions, Condition{
				Type:               string(c.Type),
				Status:             string(c.Status),
				Reason:             c.Reason,
				Message:            c.Message,
				LastTransitionTime: fmtTime(c.LastTransitionTime),
			})
		}
		out.Jobs = append(out.Jobs, info)
	}

	crons, err := t.clients.Typed.BatchV1().CronJobs(in.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, out, err
	}
	for i := range crons.Items {
		cron := &crons.Items[i]
		info := CronJobInfo{
			Name:               cron.Name,
			Schedule:           cron.Spec.Schedule,
			LastScheduleTime:   fmtTimePtr(cron.Status.LastScheduleTime),
			LastSuccessfulTime: fmtTimePtr(cron.Status.LastSuccessfulTime),
			ActiveCount:        len(cron.Status.Active),
		}
		if cron.Spec.Suspend != nil {
			info.Suspend = *cron.Spec.Suspend
		}
		out.CronJobs = append(out.CronJobs, info)
	}

	return nil, out, nil
}
