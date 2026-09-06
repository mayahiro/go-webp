# Development Guide

[English](development.md)

このガイドではgo-webpの開発で使う公開可能な検証コマンドとbenchmarkコマンドを記載します
private corpusとprivate benchmark reportはrepositoryに含めません

## 必要環境

- Go 1.26.0以上
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
make verify-external ARGS='-decoder dwebp'
make verify-external ARGS='-decoder ximage'
```

このコマンドはlossless fixtureをpixel完全一致で確認し、lossy fixtureをRGB誤差の上限で確認します
generated setにはalpha 0かつhidden RGBが非ゼロのpixelを含みます
CIはlibwebp `dwebp` 1.6.0と `golang.org/x/image/webp` v0.41.0を独立jobで実行し、各decoder versionをreportします
dwebp archiveは公式WebM release siteから取得し、SHA-256を検証します
decoder更新はencoder変更と分離し、conformance差分を個別にreviewします

container確認はRFC 9649を基準にします
VP8L構造確認はWebP Lossless Bitstream Specificationを基準とし、signature、version、transform重複、Huffman data、token上限、payload全bitの分類を検証します
decoderが受理したことだけをformat conformanceの根拠にはしません

## Fuzzing

通常の `go test` ではcommit済みseed corpusを毎回実行します
public `Encode` targetは全public mode、quality 1から100、NRGBA、RGBA、Gray、YCbCr、Paletted、opaqueとalpha、odd dimensions、non-zero origin、padding付きstride、決定的output、RIFF、VP8L、VP8、VP8X、ALPH構造を確認します
`ModeBestCompression` を繰り返し実行できるように画像を8x8以下へ制限します

追加の2 targetは同じ画像型、origin、strideを使って大きい境界を検証します
`FuzzEncodeNearLossless` は寸法1、2、3、63、64、65で前処理の閾値を通り、decode後のalpha、境界画素の保持、RGB誤差の上限を確認します
`FuzzEncodeLossyMacroblocks` は寸法15、16、17、31、32、33でmacroblock境界をまたぐ決定的outputとcontainer、VP8構造を確認します

scheduledまたはmanual GitHub Actionsでは2 workers、targetごとに5分、job timeout 15分で全4 fuzz targetを変異実行します
localでは次を実行します

```sh
go test . -run '^$' -fuzz '^FuzzEncodePublicAPI$' -fuzztime=5m -parallel=2
go test . -run '^$' -fuzz '^FuzzEncodeNearLossless$' -fuzztime=5m -parallel=2
go test . -run '^$' -fuzz '^FuzzEncodeLossyMacroblocks$' -fuzztime=5m -parallel=2
go test . -run '^$' -fuzz '^FuzzVP8LLiteralPlanRoundTrip$' -fuzztime=5m -parallel=2
```

検出したfailureを修正した後、最小化された入力を `testdata/fuzz/<target>` へ残し、通常testで再実行します
writer failureとerror propagationは全public modeを対象とする独立table-driven testで確認します

## Go Benchmark

benchmark corpusの結果を比較する際はGo toolchainのversionを固定します
[Go 1.26ではJPEG decodeが変更された](https://go.dev/doc/go1.26#imagejpeg)ため、同じJPEG fileでもGo 1.25とは画素が変わる場合があります
その結果、pixel由来のcorpus IDやtrain、holdoutの割り当ても変わる場合があります
更新時は既存manifestとbaselineを保存し、normalized pixel hashを確認してから結果を比較します
Go versionをまたいでencoderの挙動を比較する場合は、基準となるPNG corpusなどでdecode済み画素を固定します

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
make compare-lossless ARGS='-runs 3 -mode default -quality 75 -method 4'
make compare-lossless ARGS='-runs 3 -mode best -quality 100 -method 6'
make compare-lossless ARGS='-runs 3 -mode near-lossless -quality 75 -method 4'
make compare-lossless ARGS='-runs 3 -mode best -quality 100 -method 6 -corpus ../.local/corpus/production -split validation'
make compare-lossless ARGS='-runs 1 -mode default -quality 75 -method 4 -corpus ../.local/corpus/production -split validation -fixtures anonymous-id-1,anonymous-id-2'
```

通常の比較ではcwebpを `-lossless -exact` と明示的な `-q`、`-m` で実行します
標準比較はquality 75、method 4、最大圧縮寄りの比較はquality 100、method 6です

schema version 2のreportはschema version 1のfieldを維持し、次を追加します

- 1回のwarm-up後に指定run数を計測したmedian、minimum、maximumと互換性維持用average
- 決定的なoutputのSHA-256と、timed outputがwarm-upと異なる場合のreport生成拒否
- go-webp commitとdirty state、Go version、GOOS、GOARCH、GOMAXPROCS、CPU model、OS version
- 正規化した完全なcwebp arguments、cwebp quality、method、version、dwebp version
- source origin formatとcwebp input formatの分離。sourceはGoでdecode後、PNGとしてcwebpへ渡す
- pixel exact、alpha exact、encoded size、VP8L layout、source origin format別とalpha有無別aggregate

推奨split名は `development` と `validation` です
従来の `train` と `holdout` もaliasとして使用できます
`ARGS` のpathは `benchmarks` directoryから解決されます

private sourceのnameとpathはreportへ保存しません
raw per-fixture reportには画像由来の識別子とhashが含まれるため、権限 `0600` のprivate artifactとしてGitの外で管理します
terminal outputには連番placeholderを使い、fixture IDとcorpus hashを表示しません
`-fixtures` はlocalの対象限定測定だけに使い、指定IDはprivate JSON reportだけへ保存します

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
