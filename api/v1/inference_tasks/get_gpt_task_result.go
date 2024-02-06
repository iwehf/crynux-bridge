package inference_tasks

import (
	"crynux_bridge/api/v1/response"
	"crynux_bridge/config"
	"crynux_bridge/models"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type GetGPTTaskResultInput struct {
	ClientID string `path:"client_id" description:"Client id" validate:"required"`
	TaskID   uint   `path:"task_id" description:"Task id" validate:"required"`
}

type GetGPTTaskResultResponse struct {
	response.Response
	Data *models.GPTTaskResponse `json:"data"`
}

func GetGPTTaskResult(ctx *gin.Context, in *GetGPTTaskResultInput) (*GetGPTTaskResultResponse, error) {
	client := &models.Client{ClientId: in.ClientID}

	if err := config.GetDB().Where(client).First(client).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.NewValidationErrorResponse("client_id", "Client not found")
		} else {
			return nil, response.NewExceptionResponse(err)
		}
	}

	task := &models.InferenceTask{
		ClientID: client.ID,
		TaskType: models.TaskTypeLLM,
	}

	if err := config.GetDB().Where(task).First(task, in.TaskID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.NewValidationErrorResponse("task_id", "Task not found")
		} else {
			return nil, response.NewExceptionResponse(err)
		}
	}

	appConfig := config.GetConfig()
	resultFile := filepath.Join(
		appConfig.DataDir.InferenceTasks,
		strconv.FormatUint(uint64(task.ID), 10),
		"0.json",
	)

	if _, err := os.Stat(resultFile); err != nil {
		return nil, response.NewValidationErrorResponse("task_id", "Task result not ready")
	}

	resultBytes, err := os.ReadFile(resultFile)
	if err != nil {
		return nil, response.NewExceptionResponse(err)
	}

	result := &models.GPTTaskResponse{}
	if err := json.Unmarshal(resultBytes, result); err != nil {
		return nil, response.NewExceptionResponse(err)
	}

	return &GetGPTTaskResultResponse{Data: result}, nil
}
