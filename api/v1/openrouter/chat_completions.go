package openrouter

import (
	"crynux_bridge/api/v1/inference_tasks"
	"crynux_bridge/api/v1/openrouter/structs"
	"crynux_bridge/api/v1/openrouter/utils"
	"crynux_bridge/api/v1/response"
	"crynux_bridge/api/v1/tools"
	"crynux_bridge/config"
	"crynux_bridge/models"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
)

type ChatCompletionsRequest struct {
	structs.ChatCompletionsRequest
	Authorization string `header:"Authorization" validate:"required"`
}

// build TaskInput from ChatCompletionsRequest, create task, wait for task to finish, get task result, then return ChatCompletionsResponse
func ChatCompletions(c *gin.Context, in *ChatCompletionsRequest) (*structs.ChatCompletionsResponse, error) {
	ctx := c.Request.Context()
	db := config.GetDB()

	/* 1. Build TaskInput from ChatCompletionsRequest */
	in.SetDefaultValues() // set default values for some fields

	if !strings.HasPrefix(in.Authorization, "Bearer ") {
		return nil, response.NewValidationErrorResponse("Authorization", "Authorization header must start with 'Bearer '")
	}
	apiKeyStr := in.Authorization[7:]
	apiKey, err := tools.ValidateAPIKey(c.Request.Context(), config.GetDB(), apiKeyStr)
	if err != nil {
		if errors.Is(err, tools.ErrAPIKeyExpired) {
			return nil, response.NewValidationErrorResponse("Authorization", "expired")
		}
		if errors.Is(err, tools.ErrAPIKeyInvalid) {
			return nil, response.NewValidationErrorResponse("Authorization", "unauthorized")
		}
		return nil, response.NewExceptionResponse(err)
	}
	if !slices.Contains(apiKey.Roles, models.RoleAdmin) && !slices.Contains(apiKey.Roles, models.RoleChat) {
		return nil, response.NewValidationErrorResponse("Authorization", "unauthorized")
	}

	messages := make([]structs.Message, len(in.Messages))
	for i, m := range in.Messages {
		messages[i] = utils.CCReqMessageToMessage(m)
	}

	generationConfig := &structs.GPTGenerationConfig{
		MaxNewTokens:       in.MaxCompletionTokens,
		DoSample:           true,
		Temperature:        in.Temperature,
		TopP:               in.TopP,
		RepetitionPenalty:  in.FrequencyPenalty,
		NumReturnSequences: in.N,
		// NumBeams:           1,
		// TypicalP:           0.95,
		// TopK:               50,
	}

	var dtype structs.DType = structs.DTypeAuto
	if strings.HasPrefix(in.Model, "Qwen/Qwen2.5") {
		dtype = structs.DTypeBFloat16
	}

	taskArgs := structs.GPTTaskArgs{
		Model:            in.Model,
		Messages:         messages,
		Tools:            in.Tools,
		GenerationConfig: generationConfig,
		Seed:             in.Seed,
		DType:            dtype,
		// QuantizeBits:     structs.QuantizeBits8,
	}
	taskArgsStr, err := json.Marshal(taskArgs)
	if err != nil {
		err := errors.New("failed to marshal taskArgs")
		return nil, response.NewExceptionResponse(err)
	}

	taskType := models.TaskTypeLLM
	minVram := uint64(24)
	taskFee := uint64(6000000000)

	task := &inference_tasks.TaskInput{
		ClientID:        apiKey.ClientID,
		TaskArgs:        string(taskArgsStr),
		TaskType:        &taskType,
		TaskVersion:     nil,
		MinVram:         &minVram,
		RequiredGPU:     "",
		RequiredGPUVram: 0,
		RepeatNum:       nil,
		TaskFee:         &taskFee,
	}

	/* 2. Create task, wait until task finish and get task result. Implemented by function ProcessGPTTask */
	gptTaskResponse, resultDownloadedTask, err := ProcessGPTTask(ctx, db, task)
	if err != nil {
		return nil, err
	}

	/* 3. Wrap GPTTaskResponse into ChatCompletionsResponse and return */
	choices := make([]structs.CCResChoice, len(gptTaskResponse.Choices))
	for i, choice := range gptTaskResponse.Choices {
		choices[i] = utils.ResponseChoiceToCCResChoice(choice)
	}
	ccResponse := &structs.ChatCompletionsResponse{
		Id:      resultDownloadedTask.TaskID,
		Created: resultDownloadedTask.CreatedAt.Unix(),
		Model:   gptTaskResponse.Model,
		Choices: choices,
		Usage:   utils.UsageToCCResUsage(gptTaskResponse.Usage),
		// Object:  "text",
		// ServiceTier: "",
	}

	return ccResponse, nil

}
