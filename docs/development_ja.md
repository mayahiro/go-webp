# Development Guide

[English](development.md)

このガイドではgo-webpの開発で使う公開可能な検証コマンドとbenchmarkコマンドを記載します
private corpusとprivate benchmark reportはrepositoryに含めません

## 必要環境

- Go 1.25.0以上
- `cwebp` と `dwebp` は任意のlibwebp比較と外部decode確認でのみ必要

## 標準検証

```sh
make check
```

開発toolの依存は `tools` 配下、公開性能benchmarkと比較commandは `benchmarks` 配下の独立したnested moduleで管理します
`make check` はrootとbenchmark moduleのtest、vet、format確認を実行します
`tools` moduleにはformat用の `goimports` とlocal profile解析用の `pprof` が含まれます

## 外部Decoder確認

```sh
make verify-external
```

このコマンドはlossless fixtureをpixel完全一致で確認し、lossy fixtureをRGB誤差の上限で確認します
`dwebp` を優先し、利用できない場合は `go run` で一時的な `golang.org/x/image/webp` decoderを使います
どちらも利用できない場合のみmacOSの `sips` を使います

## Go Benchmark

```sh
make bench-lossy
make bench-lossless
```

計測環境、現在の結果、解釈は [Benchmarks](../BENCHMARKS.md) を参照してください

## FixtureとCorpusの補助command

```sh
make generate-fixtures
make index-corpus
```

どちらもdefaultではGit対象外の `.local` directoryへ出力します
fixture commandは決定的な公開PNG setとmanifestを生成します
corpus commandは `.local/corpus/production` 配下の画像を匿名化してindexします

## ローカルLossless比較

```sh
make compare-lossless ARGS='-runs 3 -mode default -method 4'
make compare-lossless ARGS='-runs 3 -mode best -method 6'
make compare-lossless ARGS='-runs 3 -mode near-lossless -quality 75 -method 4'
make compare-lossless ARGS='-runs 1 -mode best -method 6 -corpus ../.local/corpus/production -split holdout'
make compare-lossless ARGS='-runs 1 -mode default -method 4 -corpus ../.local/corpus/production -split holdout -fixtures anonymous-id-1,anonymous-id-2'
```

reportにはdecode後のRGB誤差とalpha一致を記録します
通常のlossless profileではpixel完全一致を必須とします
private corpusではpixel hash由来の匿名IDを使い、corpus SHA-256だけを表示してsource nameとpathは出力しません
`ARGS` のpathは `benchmarks` directoryから解決されます
`-fixtures` では生成fixture名またはprivate corpusの匿名IDを指定して調整用の再測定ができ、filter条件はJSON reportへ記録されます

## ローカルLossy Rate-Distortion比較

```sh
make compare-lossy ARGS='-runs 3 -go-mode default'
```

このコマンドには `cwebp` と `dwebp` が必要です
schema version 8のJSON reportには次を記録します

- go-webpとcwebpのquality sweep
- decode後のRGB、YUV、alpha、7x7 weighted luma SSIM、black、white、8x8 black-and-white checker背景へのRGB composite metric
- encoded sizeとVP8 partition size
- 1回のwarm-up後に指定run数を計測したmedian、minimum、maximum time
- encoded outputのSHA-256と実行時のGo version、GOOS、GOARCH、GOMAXPROCS
- encoded sizeとluma SSIM dBが最も近いfixture単位のcwebp sample
- 測定範囲の重なり内をPCHIP補間したaggregate target-sizeおよびquality-matched point
- nominal qualityおよびquality-matchedについてoverall、source format別、alpha有無別のaggregate
- fixture meanとpixel-weightedのluma SSIM、RGB、Y、UV、composite PSNR、byte-weighted rate total、alpha exact違反数
- 各Pareto curveに4点以上残る場合のRGB PSNRおよびluma SSIM BD-rate、BD-PSNR、BD-SSIM

すべてのcomparison delta fieldは次の方向とpercent基準を使用します

```text
go_minus_cwebp = go-webp - cwebp
go_minus_cwebp_percent = 100 * go_minus_cwebp / cwebp
```

size deltaが負の場合はgo-webpの出力が小さいことを表します
PSNRまたはSSIM deltaが正の場合はgo-webpのdecode後quality metricが高いことを表します
sampleのexact metricは`null`で表し、両方がexactの場合のdeltaは`0`、片側だけがexactの場合は有限差を定義できないためdeltaを`null`とします

fixture meanとpixel-weighted PSNRはfixtureごとのPSNR dB平均ではなく、それぞれのMSEを合算して計算します
byte-weighted rateはencoded bytesの合計比であり、大きいoutputほど比例して寄与します
`by_alpha.alpha` aggregateを使い、alpha付き画像の回帰がopaque画像で薄まらないようにします

fixture recordには直接測定した証跡として最近傍sample matchを残します
aggregate rate-distortion sectionではencoded bytes per pixelにshape-preserving piecewise cubic Hermite interpolation、PCHIPを使用します
target-sizeとluma SSIM matched pointは共通の測定範囲内だけをreportし、外挿は行いません
Bjontegaard積分にも同じPCHIP curveとrateの自然対数を使用します
BD-rateが負の場合は同等品質でgo-webpのbytesが少ないことを表し、BD-PSNRまたはBD-SSIMが正の場合は同等rateでgo-webpの品質が高いことを表します

`-runs` はtimed run数を指定し、1回のwarm-upを含みません
各timed outputはwarm-up outputとbyte単位で一致する必要があり、非決定的なoutputを検出した場合はreport生成を失敗させます
aggregate timing fieldはfixtureごとのmedianを合計します
output SHA-256はencoded bytes自体をJSONへ含めずに決定的な出力を識別するために使用します

`-corpus` と `-split` で非公開のlocal corpusを選択できます
reportにはsourceのnameとpathを記録しません
private inputとreportはGitの外で管理してください
`ARGS` で渡したpathは `benchmarks` directoryから解決されるため、absolute pathまたは `../` で始まるrepository相対pathを使用してください

go-webpの計測時間はprocess内の `Encode` 呼び出しだけを含みます
cwebpの計測時間にはprocess起動、PNG decode、encode、出力書き込みも含むため、cross-encoderの時間をencoder coreの順位として扱いません
reportには両方のtime totalを記録しますが、scopeが異なるためratioは計算しません
