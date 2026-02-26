package handlers

import (
	"path"
	"strings"
	"sync"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/shortcut"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

// 定义等待用户输入文件夹名称的任务状态
type PendingFolderTask struct {
	Storage      storage.Storage
	BaseDirPath  string
	Files        []tfile.TGFileMessage
	OriginalText string
	IsBatch      bool
	BotMsgID     int
}

var (
	pendingFolderTasks   = make(map[int64]PendingFolderTask)
	pendingFolderTasksMu sync.Mutex
)

// 处理用户回复文件夹名称的逻辑
func handleFolderReply(ctx *ext.Context, update *ext.Update) error {
	userID := update.GetUserChat().GetID()

	pendingFolderTasksMu.Lock()
	task, exists := pendingFolderTasks[userID]
	if !exists {
		pendingFolderTasksMu.Unlock()
		// 如果当前用户没有等待输入的任务，放行给其他 Handler 处理
		return dispatcher.ContinueGroups
	}

	folderName := strings.TrimSpace(update.EffectiveMessage.Text)

	// 如果是命令，则取消当前等待任务，交由其他 handler 处理
	if strings.HasPrefix(folderName, "/") {
		delete(pendingFolderTasks, userID)
		pendingFolderTasksMu.Unlock()
		return dispatcher.ContinueGroups
	}

	// 取出并清理任务状态
	delete(pendingFolderTasks, userID)
	pendingFolderTasksMu.Unlock()

	// 如果用户回复 ok 或空，则使用默认名称
	if strings.ToLower(folderName) == "ok" || folderName == "" {
		folderName = "TG_Download"
		if task.OriginalText != "" {
			runes := []rune(task.OriginalText)
			if len(runes) > 15 {
				folderName = string(runes[:15]) // 默认取前15个字符
			} else {
				folderName = task.OriginalText
			}
			// 替换路径非法字符
			folderName = strings.ReplaceAll(folderName, "/", "_")
			folderName = strings.ReplaceAll(folderName, "\\", "_")
			folderName = strings.ReplaceAll(folderName, "\n", " ")
		}
	}

	// 拼接最终的保存目录
	finalDirPath := path.Join(task.BaseDirPath, folderName)

	// 写入 README.txt
	if task.OriginalText != "" {
		readmePath := path.Join(finalDirPath, "README.txt")
		reader := strings.NewReader(task.OriginalText)
		err := task.Storage.Save(ctx, reader, readmePath)
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString("写入 README.txt 失败: "+err.Error()), nil)
		}
	}

	// 触发原有的下载任务，保存到新文件夹中
	if task.IsBatch {
		return shortcut.CreateAndAddBatchTGFileTaskWithEdit(ctx, userID, task.Storage, finalDirPath, task.Files, task.BotMsgID)
	}
	return shortcut.CreateAndAddTGFileTaskWithEdit(ctx, userID, task.Storage, finalDirPath, task.Files[0], task.BotMsgID)
}