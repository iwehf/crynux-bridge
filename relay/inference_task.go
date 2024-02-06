package relay

import (
	"bytes"
	"crynux_bridge/config"
	"crynux_bridge/models"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
)

type GetSDTaskResultInput struct {
	ImageNum string `json:"image_num"`
	TaskId   uint64 `json:"task_id"`
}

type GetGPTTaskResultInput struct {
	TaskId uint64 `json:"task_id"`
}

type GetGPTTaskResultResponse struct {
	Message string                 `json:"message"`
	Data    models.GPTTaskResponse `json:"data"`
}

type UploadTaskParamsInput struct {
	TaskArgs string `json:"task_args"`
	TaskId   uint64 `json:"task_id"`
}

type UploadTaskPramsWithSignature struct {
	UploadTaskParamsInput
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
}

type UploadSDResultInput struct {
	TaskId uint64 `form:"task_id" json:"task_id"`
}

type UploadGPTResultInput struct {
	TaskId uint64                 `json:"task_id"`
	Result models.GPTTaskResponse `json:"result"`
}

type UploadGPTResultInputWithSignature struct {
	UploadGPTResultInput
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
}

func UploadTask(task *models.InferenceTask) error {

	appConfig := config.GetConfig()

	params := &UploadTaskParamsInput{
		TaskArgs: task.TaskArgs,
		TaskId:   task.TaskId,
	}

	timestamp, signature, err := SignData(params, appConfig.Blockchain.Account.PrivateKey)
	if err != nil {
		return err
	}

	paramsWithSig := &UploadTaskPramsWithSignature{
		UploadTaskParamsInput: *params,
		Timestamp:             timestamp,
		Signature:             signature,
	}

	postJson, err := json.Marshal(paramsWithSig)
	if err != nil {
		return err
	}

	body := bytes.NewReader(postJson)
	reqUrl := appConfig.Relay.BaseURL + "/v1/inference_tasks"

	r, _ := http.NewRequest("POST", reqUrl, body)
	r.Header.Add("Content-Type", "application/json")
	client := &http.Client{
		Timeout: time.Duration(3) * time.Second,
	}

	response, err := client.Do(r)
	if err != nil {
		return err
	}

	if response.StatusCode != 200 {

		responseBytes, err := io.ReadAll(response.Body)

		if err != nil {
			return err
		}

		return errors.New("upload task params error: " + string(responseBytes))
	}

	return nil
}

func DownloadSDTaskResult(task *models.InferenceTask) error {

	appConfig := config.GetConfig()

	taskFolder := path.Join(
		appConfig.DataDir.InferenceTasks,
		strconv.FormatUint(uint64(task.ID), 10))

	if err := os.MkdirAll(taskFolder, 0700); err != nil {
		return err
	}

	taskIdStr := strconv.FormatUint(task.TaskId, 10)

	numImages, err := models.GetTaskConfigNumImages(task.TaskArgs)
	if err != nil {
		return err
	}

	for i := numImages - 1; i >= 0; i-- {
		iStr := strconv.Itoa(i)

		getResultInput := &GetSDTaskResultInput{
			ImageNum: strconv.Itoa(i),
			TaskId:   task.TaskId,
		}

		timestamp, signature, err := SignData(getResultInput, appConfig.Blockchain.Account.PrivateKey)
		if err != nil {
			return err
		}

		timestampStr := strconv.FormatInt(timestamp, 10)

		queryStr := "?timestamp=" + timestampStr + "&signature=" + signature
		reqUrl := appConfig.Relay.BaseURL + "/v1/inference_tasks/stable_diffusion/" + taskIdStr + "/results/" + iStr
		reqUrl = reqUrl + queryStr

		filename := path.Join(taskFolder, iStr+".png")

		log.Debugln("Downloading sd result: " + reqUrl)

		resp, err := http.Get(reqUrl)
		if err != nil {
			return err
		}

		if resp.StatusCode != 200 {
			respBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			return errors.New(string(respBytes))
		}

		file, err := os.Create(filename)
		if err != nil {
			if err := resp.Body.Close(); err != nil {
				return err
			}

			return err
		}

		_, err = io.Copy(file, resp.Body)
		if err != nil {
			if err := resp.Body.Close(); err != nil {
				return err
			}

			if err := file.Close(); err != nil {
				return err
			}

			return err
		}

		if err := resp.Body.Close(); err != nil {
			return err
		}

		if err := file.Close(); err != nil {
			return err
		}

	}

	log.Debugln("All sd results downloaded!")

	return nil
}

func DownloadGPTTaskResult(task *models.InferenceTask) (err error) {
	appConfig := config.GetConfig()

	taskFolder := path.Join(
		appConfig.DataDir.InferenceTasks,
		strconv.FormatUint(uint64(task.ID), 10))

	if err := os.MkdirAll(taskFolder, 0700); err != nil {
		return err
	}

	input := &GetGPTTaskResultInput{
		TaskId: task.TaskId,
	}

	timestamp, signature, err := SignData(input, appConfig.Blockchain.Account.PrivateKey)
	if err != nil {
		return err
	}

	queryStr := "?timestamp=" + strconv.FormatInt(timestamp, 10) + "&signature=" + signature
	reqUrl := appConfig.Relay.BaseURL + "/v1/inference_tasks/gpt/" + strconv.FormatUint(task.TaskId, 10) + "/results"
	reqUrl = reqUrl + queryStr

	log.Debugln("Downloading result: " + reqUrl)

	resp, err := http.Get(reqUrl)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, resp.Body.Close())
	}()

	if resp.StatusCode != 200 {
		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return errors.New(string(respBytes))
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	result := GetGPTTaskResultResponse{}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return err
	}

	gptRespBytes, err := json.Marshal(result.Data)
	if err != nil {
		return err
	}

	filename := filepath.Join(taskFolder, "0.json")
	if err := os.WriteFile(filename, gptRespBytes, 0700); err != nil {
		return err
	}

	log.Debug("GPT result downloaded")
	return nil
}

func UploadSDTaskResult(taskId uint64, resultFiles []io.Reader) error {

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	appConfig := config.GetConfig()

	uploadResultInput := &UploadSDResultInput{
		TaskId: taskId,
	}

	timestamp, signature, err := SignData(uploadResultInput, appConfig.Blockchain.Account.PrivateKey)
	if err != nil {
		return err
	}

	go func() {
		log.Debugln("writing form fields in go routine...")

		err = prepareUploadResultForm(resultFiles, writer, timestamp, signature)
		if err != nil {
			log.Errorln("error preparing the result uploading form")
			log.Errorln(err)

			if err2 := pw.CloseWithError(err); err2 != nil {
				log.Errorln("error closing the pipe")
				log.Errorln(err2)
			}
		}

		if err = writer.Close(); err != nil {
			log.Errorln("error closing the multipart form writer")
			log.Errorln(err)
		}

		if err = pw.Close(); err != nil {
			log.Errorln("error closing the pipe writer")
			log.Errorln(err)
		}

		log.Debugln("writing form fields completed")
	}()

	return callUploadSDResultApi(taskId, writer, pr)
}

func callUploadSDResultApi(taskId uint64, writer *multipart.Writer, body io.Reader) error {
	taskIdStr := strconv.FormatUint(taskId, 10)

	appConfig := config.GetConfig()

	reqUrl := appConfig.Relay.BaseURL + "/v1/inference_tasks/stable_diffusion/" + taskIdStr + "/results"

	req, err := http.NewRequest("POST", reqUrl, body)
	if err != nil {
		log.Errorln("error creating upload result request")
		return err
	}

	req.Header.Add("Content-Type", writer.FormDataContentType())

	client := http.Client{}

	log.Debugln("uploading results...")

	resp, err := client.Do(req)
	if err != nil {
		log.Errorln("error making upload result request")
		log.Errorln(err)

		var urlErr *url.Error
		errors.As(err, &urlErr)

		return urlErr.Unwrap()
	}

	log.Debugln("upload result api finished")

	if resp.StatusCode != 200 {
		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		return errors.New(string(respBytes))
	}

	return nil
}

func prepareUploadResultForm(
	resultFiles []io.Reader,
	writer *multipart.Writer,
	timestamp int64,
	signature string) error {
	timestampStr := strconv.FormatInt(timestamp, 10)

	if err := writer.WriteField("timestamp", timestampStr); err != nil {
		log.Errorln("error writing timestamp fields to multipart form")
		return err
	}
	if err := writer.WriteField("signature", signature); err != nil {
		log.Errorln("error writing signature fields to multipart form")
		return err
	}

	for i := 0; i < len(resultFiles); i++ {
		part, err := writer.CreateFormFile("images", "image_"+strconv.Itoa(i)+".png")
		if err != nil {
			log.Errorln("error creating form file field " + strconv.Itoa(i))
			return err
		}

		if _, err := io.Copy(part, resultFiles[i]); err != nil {
			log.Errorln("error copying image to the form field " + strconv.Itoa(i))
			return err
		}
	}

	return nil
}

func UploadGPTTaskResult(taskId uint64, result models.GPTTaskResponse) (err error) {
	input := UploadGPTResultInput{
		TaskId: taskId,
		Result: result,
	}

	appConfig := config.GetConfig()

	timestamp, signature, err := SignData(input, appConfig.Blockchain.Account.PrivateKey)
	if err != nil {
		return err
	}

	inputWithSignature := &UploadGPTResultInputWithSignature{
		UploadGPTResultInput: input,
		Timestamp:            timestamp,
		Signature:            signature,
	}

	reqBytes, err := json.Marshal(inputWithSignature)
	if err != nil {
		return err
	}
	reqBody := bytes.NewReader(reqBytes)

	reqUrl := appConfig.Relay.BaseURL + "/v1/inference_tasks/gpt/" + strconv.FormatUint(taskId, 10) + "/results"

	client := http.Client{
		Timeout: time.Duration(3) * time.Second,
	}

	resp, err := client.Post(reqUrl, "application/json", reqBody)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, resp.Body.Close())
	}()

	if resp.StatusCode != 200 {
		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		return errors.New("upload task result error" + string(respBytes))
	}

	return nil
}
