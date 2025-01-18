package tasks

import (
	"context"
	"crynux_bridge/blockchain"
	"crynux_bridge/blockchain/bindings"
	"crynux_bridge/config"
	"crynux_bridge/models"
	"crynux_bridge/relay"
	"crynux_bridge/utils"
	"crypto/rand"
	"errors"
	"math/big"
	"os"
	"path"
	"strconv"
	"sync"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	log "github.com/sirupsen/logrus"
	"github.com/vechain/go-ecvrf"
	"gorm.io/gorm"
)

func getChainTask(ctx context.Context, taskIDCommitmentBytes [32]byte) (*bindings.VSSTaskTaskInfo, error) {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return blockchain.GetTaskByCommitment(callCtx, taskIDCommitmentBytes)
}

func vrfProve(privateKey, samplingSeed []byte) ([]byte, []byte, error) {
	privKey := secp256k1.PrivKeyFromBytes(privateKey)
	beta, pi, err := ecvrf.Secp256k1Sha256Tai.Prove(privKey.ToECDSA(), samplingSeed)
	if err != nil {
		return nil, nil, err
	}
	return beta, pi, nil
}

func createTask(ctx context.Context, task *models.InferenceTask) error {
	taskIDBytes := hexutil.MustDecode(task.TaskID)
	nonceBytes := make([]byte, 32)
	rand.Read(nonceBytes)
	nonce := hexutil.Encode(nonceBytes)

	taskIDCommitmentBytes := crypto.Keccak256(append(taskIDBytes, nonceBytes...))
	taskIDCommitment := hexutil.Encode(taskIDCommitmentBytes)

	task.Nonce = nonce
	task.TaskIDCommitment = taskIDCommitment

	txHash, err := func() (string, error) {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return blockchain.CreateTaskOnChain(callCtx, task)
	}()
	if err != nil {
		return err
	}

	receipt, err := func() (*types.Receipt, error) {
		callCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		return blockchain.WaitTxReceipt(callCtx, common.HexToHash(txHash))
	}()
	if err != nil {
		return err
	}
	if receipt.Status == 0 {
		errMsg, err := blockchain.GetErrorMessageFromReceipt(ctx, receipt)
		if err != nil {
			return err
		}
		log.Errorf("ProcessTasks: %s createTaskOnChain failed: %s", task.TaskIDCommitment, errMsg)
		return errors.New(errMsg)
	}
	return nil
}

func validateSingleTask(ctx context.Context, task *models.InferenceTask) error {
	txHash, err := func() (string, error) {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return blockchain.ValidateSingleTask(callCtx, task)
	}()
	if err != nil {
		return err
	}

	receipt, err := func() (*types.Receipt, error) {
		callCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		return blockchain.WaitTxReceipt(callCtx, common.HexToHash(txHash))
	}()
	if err != nil {
		return err
	}

	if receipt.Status == 0 {
		errMsg, err := blockchain.GetErrorMessageFromReceipt(ctx, receipt)
		if err != nil {
			return err
		}
		log.Errorf("ProcessTasks: %s validateSingleTask failed: %s", task.TaskIDCommitment, errMsg)
		return errors.New(errMsg)
	}
	return nil
}

func validateTaskGroup(ctx context.Context, task1, task2, task3 *models.InferenceTask) error {
	txHash, err := func() (string, error) {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return blockchain.ValidateTaskGroup(callCtx, task1, task2, task3)
	}()
	if err != nil {
		return err
	}

	receipt, err := func() (*types.Receipt, error) {
		callCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		return blockchain.WaitTxReceipt(callCtx, common.HexToHash(txHash))
	}()
	if err != nil {
		return err
	}

	if receipt.Status == 0 {
		errMsg, err := blockchain.GetErrorMessageFromReceipt(ctx, receipt)
		if err != nil {
			return err
		}
		taskIDCommitments := []string{task1.TaskIDCommitment, task2.TaskIDCommitment, task3.TaskIDCommitment}
		log.Errorf("ProcessTasks: %s validateTaskGroup %v failed: %s", task1.TaskID, taskIDCommitments, errMsg)
		return errors.New(errMsg)
	}
	return nil
}

func syncTask(ctx context.Context, task *models.InferenceTask) (*bindings.VSSTaskTaskInfo, error) {
	taskIDCommitmentBytes, err := utils.HexStrToBytes32(task.TaskIDCommitment)
	if err != nil {
		return nil, err
	}

	chainTask, err := getChainTask(ctx, *taskIDCommitmentBytes)
	if err != nil {
		return nil, err
	}

	changed := false
	newTask := &models.InferenceTask{}
	chainTaskStatus := models.ChainTaskStatus(chainTask.Status)
	abortReason := models.TaskAbortReason(chainTask.AbortReason)
	taskError := models.TaskError(chainTask.Error)

	if abortReason != task.AbortReason {
		newTask.AbortReason = abortReason
		changed = true
	}
	if taskError != task.TaskError {
		newTask.TaskError = taskError
		changed = true
	}

	if chainTaskStatus == models.ChainTaskStarted {
		if task.Status != models.InferenceTaskStarted {
			newTask.Status = models.InferenceTaskStarted
			changed = true
		}
	} else if chainTaskStatus == models.ChainTaskParametersUploaded {
		if task.Status != models.InferenceTaskParamsUploaded {
			newTask.Status = models.InferenceTaskParamsUploaded
			changed = true
		}
	} else if chainTaskStatus == models.ChainTaskScoreReady {
		if task.Status != models.InferenceTaskScoreReady {
			newTask.Status = models.InferenceTaskScoreReady
			changed = true
		}
	} else if chainTaskStatus == models.ChainTaskErrorReported {
		if task.Status != models.InferenceTaskErrorReported {
			newTask.Status = models.InferenceTaskErrorReported
			changed = true
		}
	} else if chainTaskStatus == models.ChainTaskValidated || chainTaskStatus == models.ChainTaskGroupValidated {
		if task.Status != models.InferenceTaskValidated {
			newTask.Status = models.InferenceTaskValidated
			changed = true
		}
	} else if chainTaskStatus == models.ChainTaskEndAborted {
		if task.Status != models.InferenceTaskEndAborted {
			newTask.Status = models.InferenceTaskEndAborted
			changed = true
		}
	} else if chainTaskStatus == models.ChainTaskEndInvalidated {
		if task.Status != models.InferenceTaskEndInvalidated {
			newTask.Status = models.InferenceTaskEndInvalidated
			changed = true
		}
	} else if chainTaskStatus == models.ChainTaskEndGroupRefund {
		if task.Status != models.InferenceTaskEndGroupRefund {
			newTask.Status = models.InferenceTaskEndGroupRefund
			changed = true
		}
	} else if chainTaskStatus == models.ChainTaskEndSuccess || chainTaskStatus == models.ChainTaskEndGroupSuccess {
		if task.Status != models.InferenceTaskEndSuccess {
			newTask.Status = models.InferenceTaskEndSuccess
			changed = true
		}
	}

	if changed {
		if err := task.Update(ctx, config.GetDB(), newTask); err != nil {
			return nil, err
		}
	}
	return chainTask, nil
}

func processOneTask(ctx context.Context, task *models.InferenceTask) error {
	// sync task from blockchain first
	_, err := syncTask(ctx, task)
	if err != nil {
		return err
	}

	// report task params is uploaded to blochchain
	if task.Status == models.InferenceTaskPending {
		if err := createTask(ctx, task); err != nil {
			return err
		}

		newTask := &models.InferenceTask{
			Status:           models.InferenceTaskCreated,
			Nonce:            task.Nonce,
			TaskIDCommitment: task.TaskIDCommitment,
		}

		// get task sequence and sampling number
		taskIDCommitmentBytes, err := utils.HexStrToBytes32(task.TaskIDCommitment)
		if err != nil {
			return err
		}

		chainTask, err := getChainTask(ctx, *taskIDCommitmentBytes)
		if err != nil {
			return err
		}
		newTask.Sequence = chainTask.Sequence.Uint64()

		subTasks := make([]*models.InferenceTask, 0)

		// validation tasks' sampling seed is not empty
		// avoid generating validation tasks for validation tasks
		if len(task.SamplingSeed) == 0 {
			newTask.SamplingSeed = hexutil.Encode(chainTask.SamplingSeed[:])
			// generate vrf proof
			appConfig := config.GetConfig()
			pk := appConfig.Blockchain.Account.PrivateKey
			privateKey, err := hexutil.Decode("0x" + pk)
			if err != nil {
				log.Errorf("ProcessTasks: %s decode private key failed: %v", task.TaskIDCommitment, err)
				return err
			}
			vrfProof, vrfNum, err := vrfProve(privateKey, chainTask.SamplingSeed[:])
			if err != nil {
				log.Errorf("ProcessTasks: %s vrf prove failed: %v", task.TaskIDCommitment, err)
				return err
			}
			newTask.VRFProof = hexutil.Encode(vrfProof)
			newTask.VRFNumber = hexutil.Encode(vrfNum)

			number := big.NewInt(0).SetBytes(vrfNum)
			r := big.NewInt(0).Mod(number, big.NewInt(10)).Uint64()
			// if vrfNumber % 10 == 0, create 2 validation tasks
			if r == 0 {
				for i := 0; i < 2; i++ {
					subTask := &models.InferenceTask{
						ClientID:        task.ClientID,
						ClientTaskID:    task.ClientTaskID,
						TaskArgs:        task.TaskArgs,
						TaskType:        task.TaskType,
						TaskModelIDs:    task.TaskModelIDs,
						TaskVersion:     task.TaskVersion,
						TaskFee:         task.TaskFee,
						MinVram:         task.MinVram,
						RequiredGPU:     task.RequiredGPU,
						RequiredGPUVram: task.RequiredGPUVram,
						TaskSize:        task.TaskSize,
						TaskID:          task.TaskID,
						SamplingSeed:    newTask.SamplingSeed,
						VRFProof:        newTask.VRFProof,
						VRFNumber:       newTask.VRFNumber,
					}
					subTasks = append(subTasks, subTask)
				}
			}
		}

		err = config.GetDB().Transaction(func(tx *gorm.DB) error {
			if err := task.Update(ctx, tx, newTask); err != nil {
				return err
			}
			if err := models.SaveTasks(ctx, tx, subTasks); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
		log.Infof("ProcessTasks: create task %s on chain", task.TaskIDCommitment)
	}

	// upload task params to relay when task starts
	if task.Status == models.InferenceTaskCreated {
		if err := relay.UploadTask(ctx, task); err != nil {
			log.Errorf("ProcessTasks: relay upload task %s error: %v", task.TaskIDCommitment, err)
			return err
		}

		newTask := &models.InferenceTask{
			Status: models.InferenceTaskParamsUploaded,
		}
		if err := task.Update(ctx, config.GetDB(), newTask); err != nil {
			return err
		}
		log.Infof("ProcessTasks: upload params of task %s", task.TaskIDCommitment)
	}

	// wait task status to be score ready, error reported or abort
	if task.Status == models.InferenceTaskParamsUploaded {
		for {
			_, err := syncTask(ctx, task)
			if err != nil {
				return err
			}
			if task.Status == models.InferenceTaskScoreReady || task.Status == models.InferenceTaskErrorReported || task.Status == models.InferenceTaskEndAborted {
				break
			}
			time.Sleep(time.Second)
		}
		log.Infof("ProcessTasks: task %s status %d", task.TaskIDCommitment, task.Status)
	}

	if task.Status == models.InferenceTaskEndAborted {
		log.Errorf("ProcessTasks: task %s aborted for reason: %d", task.TaskIDCommitment, task.AbortReason)
		return nil
	}

	if task.Status == models.InferenceTaskScoreReady || task.Status == models.InferenceTaskErrorReported {
		needValidate := false
		taskGroup, err := models.GetTaskGroup(ctx, config.GetDB(), task.TaskID)
		if err != nil {
			log.Errorf("ProcessTasks: get tasks of task id %s error: %v", task.TaskID, err)
			return err
		}
		if len(taskGroup) == 1 {
			needValidate = true
		} else if len(taskGroup) == 3 {
			// wait all tasks in group be in status score ready, error reported or aborted
			validateTaskIDCommitment := ""
			for {
				readyCount := 0
				for _, subTask := range taskGroup {
					if subTask.Status == models.InferenceTaskScoreReady || subTask.Status == models.InferenceTaskErrorReported || subTask.Status == models.InferenceTaskEndAborted {
						readyCount += 1
						if subTask.Status != models.InferenceTaskEndAborted && len(validateTaskIDCommitment) == 0 {
							validateTaskIDCommitment = subTask.TaskIDCommitment
						}
					}
				}
				if readyCount == 3 {
					break
				}
				taskGroup, err = models.GetTaskGroup(ctx, config.GetDB(), task.TaskID)
				if err != nil {
					log.Errorf("ProcessTasks: get tasks of %s error: %v", task.TaskID, err)
					return err
				}
			}
			if task.TaskIDCommitment == validateTaskIDCommitment {
				needValidate = true
			}
		}

		// validate task
		if needValidate {
			if len(taskGroup) == 1 {
				if err := validateSingleTask(ctx, task); err != nil {
					return err
				}
				log.Infof("ProcessTasks: validate single task %s", task.TaskIDCommitment)
			} else if len(taskGroup) == 3 {
				if err := validateTaskGroup(ctx, &taskGroup[0], &taskGroup[1], &taskGroup[2]); err != nil {
					return err
				}
				log.Infof("ProcessTasks: validate task group task %s, %s, %s", taskGroup[0].TaskIDCommitment, taskGroup[1].TaskIDCommitment, taskGroup[2].TaskIDCommitment)
			}
		}

		// wait task status to be validated, invalidated, success, group refund or aborted
		for {
			_, err := syncTask(ctx, task)
			if err != nil {
				return err
			}
			if task.Status == models.InferenceTaskValidated || task.Status == models.InferenceTaskValidated || task.Status == models.InferenceTaskEndSuccess || task.Status == models.InferenceTaskEndGroupRefund || task.Status == models.InferenceTaskEndAborted {
				break
			}
			time.Sleep(time.Second)
		}
		log.Infof("ProcessTasks: task %s status %d", task.TaskIDCommitment, task.Status)
	}

	// download task result
	if task.Status == models.InferenceTaskValidated || task.Status == models.InferenceTaskEndSuccess {
		appConfig := config.GetConfig()

		taskFolder := path.Join(
			appConfig.DataDir.InferenceTasks,
			task.TaskIDCommitment,
		)

		if err := os.MkdirAll(taskFolder, 0700); err != nil {
			log.Errorf("ProcessTasks: cannot create task result dir of %s", task.TaskIDCommitment)
			return err
		}

		ext := "png"
		if task.TaskType == models.TaskTypeLLM {
			ext = "json"
		}

		var wg sync.WaitGroup
		errCh := make(chan error)
		for i := 0; i < int(task.TaskSize); i++ {
			wg.Add(1)
			go func(index int64) {
				defer wg.Done()
				filename := path.Join(taskFolder, strconv.FormatInt(index, 10)+"."+ext)
				file, err := os.Create(filename)
				if err != nil {
					log.Errorf("ProcessTasks: create task result file %d of %s error: %v", index, task.TaskIDCommitment, err)
					errCh <- err
				}
				defer file.Close()
				ctx1, cancel := context.WithTimeout(ctx, time.Minute)
				defer cancel()
				if err := relay.DownloadTaskResult(ctx1, task, index, file); err != nil {
					log.Errorf("ProcessTasks: download task result %d of %s error: %v", index, task.TaskIDCommitment, err)
					errCh <- err
				}
				errCh <- nil
			}(int64(i))
		}
		wg.Wait()
		for err := range errCh {
			if err != nil {
				return err
			}
		}
		newTask := &models.InferenceTask{
			Status: models.InferenceTaskResultDownloaded,
		}
		if err := task.Update(ctx, config.GetDB(), newTask); err != nil {
			return err
		}
		log.Infof("ProcessTasks: download results of task %s", task.TaskIDCommitment)
	}
	return nil
}

func ProcessTasks(ctx context.Context) {
	limit := 100
	lastID := uint(0)

	interval := 1

	for {
		tasks, err := func(ctx context.Context) ([]models.InferenceTask, error) {
			var tasks []models.InferenceTask

			dbCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			err := config.GetDB().WithContext(dbCtx).Model(&models.InferenceTask{}).
				Where("status != ?", models.InferenceTaskEndAborted).
				Where("status != ?", models.InferenceTaskEndInvalidated).
				Where("status != ?", models.InferenceTaskEndGroupRefund).
				Where("status != ?", models.InferenceTaskResultDownloaded).
				Where("id > ?", lastID).
				Find(&tasks).
				Order("id ASC").
				Limit(limit).
				Error
			if err != nil {
				return nil, err
			}
			return tasks, nil
		}(ctx)
		if err != nil {
			log.Errorf("ProcessTasks: cannot get unprocessed tasks: %v", err)
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		}

		if len(tasks) > 0 {
			lastID = tasks[len(tasks)-1].ID

			for _, task := range tasks {
				go func(ctx context.Context, task models.InferenceTask) {
					log.Infof("ProcessTasks: start processing task %s", task.TaskIDCommitment)
					var ctx1 context.Context
					var cancel context.CancelFunc
					deadline := task.CreatedAt.Add(10 * time.Minute)
					ctx1, cancel = context.WithDeadline(ctx, deadline)
					defer cancel()

					for {
						c := make(chan error, 1)
						go func() {
							c <- processOneTask(ctx1, &task)
						}()

						select {
						case err := <-c:
							if err != nil {
								log.Errorf("ProcessTasks: process task %s error %v, retry", task.TaskIDCommitment, err)
							} else {
								log.Infof("ProcessTasks: process task %s successfully", task.TaskIDCommitment)
								return
							}
						case <-ctx1.Done():
							err := ctx1.Err()
							log.Errorf("ProcessTasks: process task %s timeout %v, finish", task.TaskIDCommitment, err)
							// set task status to aborted to avoid processing it again
							if err == context.DeadlineExceeded {
								newTask := &models.InferenceTask{Status: models.InferenceTaskEndAborted}
								if err := task.Update(ctx, config.GetDB(), newTask); err != nil {
									log.Errorf("ProcessTasks: save task %s error %v", task.TaskIDCommitment, err)
								}
							}
							return
						}
					}
				}(ctx, task)
			}
		}

		time.Sleep(time.Second)
	}
}