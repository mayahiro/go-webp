# Development Guide

[English](development.md)

このガイドではgo-webpの開発で使う公開可能な検証コマンドとbenchmarkコマンドを記載します
private corpusとprivate benchmark reportはrepositoryに含めません

## 必要環境

- Go 1.25.0以上
- `cwebp` と `dwebp` は任意のlibwebp比較と外部decode確認でのみ必要

## 標準検証

```sh
go test ./...
go vet ./...
go -C tools tool goimports -l ..
```

開発toolの依存は `tools` 配下の独立したnested moduleで固定します
repository rootから `go -C tools tool` で実行することで、ライブラリmoduleの依存graphから分離します
このmoduleにはformat用の `goimports` とlocal profile解析用の `pprof` が含まれます

## 外部Decoder確認

```sh
go run ./scripts/verify_lossless_external
```

このコマンドはlossless fixtureをpixel完全一致で確認し、lossy fixtureをRGB誤差の上限で確認します
`dwebp` を優先し、利用できない場合は `go run` で一時的な `golang.org/x/image/webp` decoderを使います
どちらも利用できない場合のみmacOSの `sips` を使います

## Go Benchmark

```sh
go test . -run '^$' \
  -bench '^BenchmarkEncodeLossyFixtures$' \
  -benchmem -benchtime=3x -count=3

go test . -run '^$' \
  -bench '^BenchmarkEncodeLosslessSmallFixtures$' \
  -benchmem -benchtime=3x -count=3
```

計測環境、現在の結果、解釈は [Benchmarks](../BENCHMARKS.md) を参照してください

## ローカルLossless比較

```sh
go run ./scripts/compare_lossless_libwebp -runs 3 -mode default -method 4
go run ./scripts/compare_lossless_libwebp -runs 3 -mode best -method 6
go run ./scripts/compare_lossless_libwebp -runs 3 -mode near-lossless -quality 75 -method 4
```

reportにはdecode後のRGB誤差とalpha一致を記録します
通常のlossless profileではpixel完全一致を必須とします

## ローカルLossy Rate-Distortion比較

```sh
go run ./scripts/compare_lossy_libwebp \
  -runs 3 \
  -go-mode default \
  -json report.json
```

このコマンドには `cwebp` と `dwebp` が必要です
schema version 3のJSON reportには次を記録します

- go-webpとcwebpのquality sweep
- decode後のRGB、YUV、alpha、7x7 weighted luma SSIM
- encoded sizeとVP8 partition size
- encode時間
- encoded sizeとluma SSIM dBが最も近いcwebp sample

`-corpus` と `-split` で非公開のlocal corpusを選択できます
reportにはsourceのnameとpathを記録しません
private inputとreportはGitの外で管理してください

go-webpの計測時間はprocess内の `Encode` 呼び出しだけを含みます
cwebpの計測時間にはprocess起動、PNG decode、encode、出力書き込みも含むため、cross-encoderの時間をencoder coreの順位として扱いません
