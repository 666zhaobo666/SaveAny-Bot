package handlers

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/msgelem"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/shortcut"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/common/utils/fsutil"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/tcbdata"
	"github.com/krau/SaveAny-Bot/storage"
	"gorm.io/gorm"
)

func handleAddCallback(ctx *ext.Context, update *ext.Update) error {
	dataid := strings.Split(string(update.CallbackQuery.Data), " ")[1]
	data, err := shortcut.GetCallbackDataWithAnswer[tcbdata.Add](ctx, update, dataid)
	if err != nil {
		return err
	}
	queryID := update.CallbackQuery.GetQueryID()
	msgID := update.CallbackQuery.GetMsgID()
	userID := update.CallbackQuery.GetUserID()

	selectedStorage, err := storage.GetStorageByUserIDAndName(ctx, userID, data.SelectedStorName)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to get storage: %s", err)
		ctx.AnswerCallback(msgelem.AlertCallbackAnswer(queryID, i18n.T(i18nk.BotMsgCommonErrorGetStorageFailed, map[string]any{
			"Error": err.Error(),
		})))
		return dispatcher.EndGroups
	}
	dirs, err := database.GetDirsByUserChatIDAndStorageName(ctx, userID, data.SelectedStorName)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to get user directories: %w", err)
	}

	if !data.SettedDir && len(dirs) != 0 {
		// ask for directory selection
		markup, err := msgelem.BuildSetDirMarkupForAdd(dirs, dataid)
		if err != nil {
			log.FromContext(ctx).Errorf("Failed to build directory keyboard: %s", err)
			ctx.AnswerCallback(msgelem.AlertCallbackAnswer(queryID, i18n.T(i18nk.BotMsgCommonErrorBuildStorageSelectKeyboardFailed, map[string]any{
				"Error": err.Error(),
			})))
			return dispatcher.EndGroups
		}
		ctx.EditMessage(userID, &tg.MessagesEditMessageRequest{
			ID:          update.CallbackQuery.GetMsgID(),
			Message:     i18n.T(i18nk.BotMsgCommonPromptSelectDir, nil),
			ReplyMarkup: markup,
		})
		return dispatcher.EndGroups
	}

	dirPath := ""
	if data.DirID != 0 {
		dir, err := database.GetDirByID(ctx, data.DirID)
		if err != nil {
			ctx.AnswerCallback(msgelem.AlertCallbackAnswer(queryID, i18n.T(i18nk.BotMsgCommonErrorGetDirFailed, map[string]any{
				"Error": err.Error(),
			})))
			return dispatcher.EndGroups
		}
		dirPath = dir.Path
	}

	switch data.TaskType {
	case tasktype.TaskTypeTgfiles:
		// 1. 尝试获取原始消息的文本内容
		var originalText string
		botMsg, ok := update.CallbackQuery.Message.AsNotEmpty()
		if ok && botMsg.GetReplyTo() != nil {
			origMsgID := botMsg.GetReplyTo().GetReplyToMsgID()
			res, err := ctx.API().MessagesGetMessages(ctx, &tg.MessagesGetMessagesRequest{
				ID: []tg.InputMessageClass{&tg.InputMessageID{ID: origMsgID}},
			})
			if err == nil {
				if msgs, ok := res.(tg.MessageClassArray); ok && len(msgs) > 0 {
					if m, ok := msgs[0].(*tg.Message); ok {
						originalText = m.Message
					}
				}
			}
		}

		// 2. 生成提示用的默认名称
		defaultName := "TG_Download"
		if originalText != "" {
			runes := []rune(originalText)
			if len(runes) > 15 {
				defaultName = string(runes[:15])
			} else {
				defaultName = originalText
			}
			defaultName = strings.ReplaceAll(defaultName, "\n", " ")
		}

		// 3. 将任务存入缓存
		pendingFolderTasksMu.Lock()
		pendingFolderTasks[userID] = PendingFolderTask{
			Storage:      selectedStorage,
			BaseDirPath:  dirPath,
			Files:        data.Files,
			OriginalText: originalText,
			IsBatch:      data.AsBatch,
			BotMsgID:     msgID,
		}
		pendingFolderTasksMu.Unlock()

		// 4. 发送询问消息 (ForceReply 强制用户回复)
		ctx.SendMessage(userID, &tg.MessagesSendMessageRequest{
			Message: "📁 请输入要保存的文件夹名称（直接回复此消息）：\n\n💡 回复 `ok` 将使用默认名称: \n`" + defaultName + "`",
			ReplyTo: &tg.InputReplyToMessage{
				ReplyToMsgID: botMsg.ID,
			},
		})
		return dispatcher.EndGroups
	case tasktype.TaskTypeTphpics:
		return shortcut.CreateAndAddtelegraphWithEdit(ctx, userID, data.TphPageNode, data.TphDirPath, data.TphPics, selectedStorage, msgID)
	case tasktype.TaskTypeParseditem:
		if len(data.ParsedItem.Resources) > 1 {
			dirPath = path.Join(dirPath, fsutil.NormalizePathname(data.ParsedItem.Title))
		}
		shortcut.CreateAndAddParsedTaskWithEdit(ctx, selectedStorage, dirPath, data.ParsedItem, msgID, userID)
	case tasktype.TaskTypeDirectlinks:
		shortcut.CreateAndAddDirectTaskWithEdit(ctx, selectedStorage, dirPath, data.DirectLinks, msgID, userID)
	case tasktype.TaskTypeAria2:
		client := GetAria2Client()
		if client == nil {
			ctx.AnswerCallback(msgelem.AlertCallbackAnswer(queryID, i18n.T(i18nk.BotMsgAria2ErrorAria2ClientInitFailed, map[string]any{
				"Error": "aria2 client not initialized",
			})))
			return dispatcher.EndGroups
		}
		shortcut.CreateAndAddAria2TaskWithEdit(ctx, selectedStorage, dirPath, data.Aria2URIs, client, msgID, userID)
	case tasktype.TaskTypeYtdlp:
		shortcut.CreateAndAddYtdlpTaskWithEdit(ctx, selectedStorage, dirPath, data.YtdlpURLs, data.YtdlpFlags, msgID, userID)
	case tasktype.TaskTypeTransfer:
		return handleTransferCallback(ctx, userID, selectedStorage, dirPath, data, msgID)
	default:
		return fmt.Errorf("unexcept task type: %s", data.TaskType)
	}
	return dispatcher.EndGroups
}
