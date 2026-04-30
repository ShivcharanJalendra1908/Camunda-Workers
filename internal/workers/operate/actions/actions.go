package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/camunda/zeebe/clients/go/v8/pkg/pb"
	"github.com/camunda/zeebe/clients/go/v8/pkg/zbc"

	"camunda-workers/internal/models"
)

// OperateActionService wraps Zeebe gRPC write operations needed by Operate.
// Uses the same zbc.Client from internal/common/camunda/client.go — no proto needed.
type OperateActionService struct {
	client zbc.Client
}

// NewOperateActionService accepts the raw zbc.Client from your existing camunda.Client.
//
// Usage in main.go:
//
//	operateActions := actions.NewOperateActionService(camundaClient.GetClient())
func NewOperateActionService(client zbc.Client) *OperateActionService {
	return &OperateActionService{client: client}
}

// ResolveIncident — call AFTER fixing root cause (update retries or variables first).
func (s *OperateActionService) ResolveIncident(ctx context.Context, incidentKey int64) error {
	_, err := s.client.NewResolveIncidentCommand().
		IncidentKey(incidentKey).
		Send(ctx)
	if err != nil {
		return fmt.Errorf("resolve incident %d: %w", incidentKey, err)
	}
	return nil
}

// UpdateJobRetries — resets retries so a failed job becomes executable again.
func (s *OperateActionService) UpdateJobRetries(ctx context.Context, jobKey int64, retries int32) error {
	_, err := s.client.NewUpdateJobRetriesCommand().
		JobKey(jobKey).
		Retries(int32(retries)).
		Send(ctx)
	if err != nil {
		return fmt.Errorf("update retries job %d: %w", jobKey, err)
	}
	return nil
}

// CancelProcessInstance — immediately stops a running workflow instance.
func (s *OperateActionService) CancelProcessInstance(ctx context.Context, instanceKey int64) error {
	_, err := s.client.NewCancelInstanceCommand().
		ProcessInstanceKey(instanceKey).
		Send(ctx)
	if err != nil {
		return fmt.Errorf("cancel instance %d: %w", instanceKey, err)
	}
	return nil
}

// SetVariables — updates variables on a running workflow scope.
// local=true means only that element's scope gets updated.
func (s *OperateActionService) SetVariables(
	ctx context.Context,
	scopeKey int64,
	variables map[string]interface{},
	local bool,
) error {
	varsJSON, err := json.Marshal(variables)
	if err != nil {
		return fmt.Errorf("marshal variables: %w", err)
	}

	cmd, err := s.client.NewSetVariablesCommand().
		ElementInstanceKey(scopeKey).
		VariablesFromString(string(varsJSON))
	if err != nil {
		return fmt.Errorf("build set variables command: %w", err)
	}

	if local {
		cmd = cmd.Local(true)
	}

	_, err = cmd.Send(ctx)
	if err != nil {
		return fmt.Errorf("set variables scope %d: %w", scopeKey, err)
	}
	return nil
}

// ModifyProcessInstance — moves tokens between flow nodes.
// Used for workflow repair / operational intervention. (Zeebe 8.x)
func (s *OperateActionService) ModifyProcessInstance(
	ctx context.Context,
	req models.ModifyInstanceRequest,
	instanceKey int64,
) error {
	grpcReq := &pb.ModifyProcessInstanceRequest{
		ProcessInstanceKey: instanceKey,
	}

	if req.ActivateElementID != "" {
		grpcReq.ActivateInstructions = []*pb.ModifyProcessInstanceRequest_ActivateInstruction{
			{ElementId: req.ActivateElementID},
		}
	}
	if req.TerminateElementInstanceKey != 0 {
		grpcReq.TerminateInstructions = []*pb.ModifyProcessInstanceRequest_TerminateInstruction{
			{ElementInstanceKey: req.TerminateElementInstanceKey},
		}
	}

	if gc, ok := s.client.(interface{ GatewayClient() pb.GatewayClient }); ok {
		_, err := gc.GatewayClient().ModifyProcessInstance(ctx, grpcReq)
		if err != nil {
			return fmt.Errorf("modify instance %d: %w", instanceKey, err)
		}
		return nil
	}

	return fmt.Errorf("zeebe client does not support ModifyProcessInstance (GatewayClient missing)")
}

// UpdateJobTimeout — extends a running job's timeout to avoid reassignment. (Zeebe 8.x)
func (s *OperateActionService) UpdateJobTimeout(ctx context.Context, jobKey int64, timeoutMs int64) error {
	if gc, ok := s.client.(interface{ GatewayClient() pb.GatewayClient }); ok {
		_, err := gc.GatewayClient().UpdateJobTimeout(ctx, &pb.UpdateJobTimeoutRequest{
			JobKey:  jobKey,
			Timeout: timeoutMs,
		})
		if err != nil {
			return fmt.Errorf("update timeout job %d: %w", jobKey, err)
		}
		return nil
	}

	return fmt.Errorf("zeebe client does not support UpdateJobTimeout (GatewayClient missing)")
}
