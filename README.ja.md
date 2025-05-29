# livinglock

[English](README.md) | **日本語**

Go言語で**スケジュールバッチ処理**専用に設計された堅牢なファイルベースプロセスロック機構です。従来のPIDロックとは異なり、`livinglock`はアクティブなハートビート監視によりデッドロックしたプロセスを検出します。

## 解決する問題

従来のバッチジョブ排他制御手法は現実のシナリオで失敗します：

- **単純なPIDロック**: デッドロックや無限ループで停止したプロセスを検出できない
- **時間ベースロック**: 健全な長時間実行プロセスを誤って終了させる
- **ロックなし**: 有害な並行実行を許可してしまう

`livinglock`はロック保持者からの**アクティブな作業証明シグナル**を要求し、プロセスの存在だけでなく実際の継続作業を保証します。

## クイックスタート

### インストール

```bash
go get github.com/ideamans/living-lock
```

### 基本的な使用方法

```go
package main

import (
    "errors"
    "log"
    "time"
    
    "github.com/ideamans/living-lock"
)

func main() {
    // バッチ処理用のロック取得
    lock, err := livinglock.Acquire("/tmp/daily-batch.lock", livinglock.Options{
        StaleTimeout:             2 * time.Hour,  // 最大予想実行時間
        HeartBeatMinimalInterval: 5 * time.Minute, // ハートビート頻度
    })
    if err != nil {
        if errors.Is(err, livinglock.ErrLockBusy) {
            log.Printf("前回のバッチがまだ実行中、優先権を譲ります")
            return // 正常終了
        }
        log.Fatalf("ロック取得に失敗: %v", err)
    }
    defer lock.Release()

    log.Printf("バッチ処理を開始...")
    
    // 定期的なハートビートと共に作業を処理
    for i := 0; i < 10; i++ {
        // 実際の作業を実行
        processDataBatch(i)
        
        // アクティブに作業中であることを通知（デッドロックしていない証明）
        if err := lock.HeartBeat(); err != nil {
            log.Printf("ハートビート失敗: %v", err)
            return
        }
        
        log.Printf("バッチ %d 完了", i)
    }
    
    log.Printf("バッチ処理が正常完了")
}

func processDataBatch(id int) {
    // 作業をシミュレート
    time.Sleep(30 * time.Second)
}
```

## 動作原理

### 処理されるシナリオ

1. **正常ケース**: 新しいcronジョブ開始、前回ジョブは完了済み → 正常実行
2. **アクティブプロセス**: 前回ジョブがハートビートと共に作業中 → 新ジョブは正常に譲歩
3. **デッドロックプロセス**: 前回ジョブがハートビートなしで停止 → 新ジョブが引き継ぎ
4. **クラッシュプロセス**: 前回ジョブが終了済み → 新ジョブが即座にロック取得

### ロック状態

```bash
# 健全: プロセス存在 かつ ハートビート送信 → 干渉しない
# デッドロック: プロセス存在 しかし ハートビートなし → 安全に引き継ぎ
# クラッシュ: プロセス不存在 → 安全に引き継ぎ
```

## API リファレンス

### `Acquire(filePath string, options Options) (*Lock, error)`

指定されたファイルパスでロックの取得を試みます。

**戻り値:**
- `*Lock`: 成功時のロックインスタンス
- `ErrLockBusy`: 他のアクティブプロセスがロックを保持
- `error`: 予期しないエラー（ファイル権限、ディスク容量等）

### `Lock.HeartBeat() error`

プロセスがアクティブに作業中であることを証明するハートビートシグナルを送信。長時間実行作業中に定期的に呼び出してください。

### `Lock.Release() error`

ロックを解放し、ロックファイルを削除。複数回呼び出しても安全です。

### オプション

```go
type Options struct {
    StaleTimeout             time.Duration // ロックを古いと判定する時間（デフォルト: 1時間）
    HeartBeatMinimalInterval time.Duration // ハートビート間の最小間隔（デフォルト: 1分）
    Dependencies             *Dependencies // テスト用（オプション）
}
```

## ベストプラクティス

### Cronジョブ

```go
// cron スケジュールされたバッチジョブ内
func main() {
    lock, err := livinglock.Acquire("/var/run/backup.lock", livinglock.Options{
        StaleTimeout: 4 * time.Hour, // 最大バックアップ時間
    })
    if err != nil {
        if errors.Is(err, livinglock.ErrLockBusy) {
            return // 前回のバックアップがまだ実行中
        }
        log.Fatal(err)
    }
    defer lock.Release()
    
    // 定期的な HeartBeat() 呼び出しと共にバックアップロジック
}
```

### ハートビート頻度

- 作業中に`HeartBeat()`を定期的に呼び出す
- 頻度は`StaleTimeout`よりもずっと短くする
- 作業の自然なチェックポイント間隔を考慮する

### エラーハンドリング

- `ErrLockBusy`: 期待される動作、正常に処理する
- その他のエラー: 通常はシステム問題（権限、ディスク容量）を示す

## テスト

パッケージには実際のマルチプロセスシナリオを含む包括的なテストが含まれています：

```bash
go test ./...
```

## ライセンス

MIT License - 詳細は [LICENSE](LICENSE) ファイルをご覧ください。

## コントリビューション

コントリビューションを歓迎します！コントリビューションガイドラインをお読みいただき、すべてのテストが通ることを確認してください。

## 使用例: 実際のバッチ処理

### データベースバックアップ

```go
func dailyBackup() {
    lock, err := livinglock.Acquire("/var/run/db-backup.lock", livinglock.Options{
        StaleTimeout: 6 * time.Hour, // 大規模DBは時間がかかる場合がある
        HeartBeatMinimalInterval: 10 * time.Minute,
    })
    if err != nil {
        if errors.Is(err, livinglock.ErrLockBusy) {
            log.Printf("前回のバックアップが継続中")
            return
        }
        log.Fatalf("バックアップ開始失敗: %v", err)
    }
    defer lock.Release()

    databases := []string{"users", "orders", "products", "analytics"}
    
    for _, db := range databases {
        log.Printf("データベース %s のバックアップ開始", db)
        
        // 実際のバックアップ処理（時間がかかる可能性）
        if err := backupDatabase(db); err != nil {
            log.Printf("バックアップ失敗 %s: %v", db, err)
            return
        }
        
        // 作業が継続中であることを通知
        if err := lock.HeartBeat(); err != nil {
            log.Printf("ハートビート失敗、別プロセスが引き継ぎ可能性: %v", err)
            return
        }
        
        log.Printf("データベース %s のバックアップ完了", db)
    }
    
    log.Printf("全データベースのバックアップが正常完了")
}
```

### ログ集約処理

```go
func aggregateLogs() {
    lock, err := livinglock.Acquire("/tmp/log-aggregation.lock", livinglock.Options{
        StaleTimeout: 3 * time.Hour,
        HeartBeatMinimalInterval: 5 * time.Minute,
    })
    if err != nil {
        if errors.Is(err, livinglock.ErrLockBusy) {
            log.Printf("ログ集約が既に実行中")
            return
        }
        log.Fatalf("ログ集約開始失敗: %v", err)
    }
    defer lock.Release()

    logSources := []string{"/var/log/app1", "/var/log/app2", "/var/log/nginx"}
    
    for _, source := range logSources {
        if err := processLogSource(source); err != nil {
            log.Printf("ログソース処理失敗 %s: %v", source, err)
            continue
        }
        
        // 定期的なハートビート
        lock.HeartBeat()
    }
}
```