# go-webp

[English](README.md)

go-webpはGo標準の `image.Image` インターフェイス向けに、画質と圧縮率を重視したWebP encoding libraryです
Go標準の画像encoderに近い小さなAPIで、静止画のVP8L lossless、VP8L near-lossless、VP8 lossyを書き出します

## 特徴

- cgo、native library、architecture固有assemblyを使わないpure Go実装
- lossless、near-lossless、true VP8 lossy encoding
- すべてのcompression familyでalphaを保持
- 専用のエンコード関数による任意のICC、Exif、XMP metadata付与
- fast、balanced、best-compression、auto、low-memoryのcompression profile
- 主要な標準画像型の専用経路と、一般的な `image.Image` fallback
- `EncodeContext` と `Encoder.EncodeContext` による協調的なキャンセル
- Go 1.26.0以上

go-webpは静止画encodingに責務を限定しています
decodeには [`golang.org/x/image/webp`](https://pkg.go.dev/golang.org/x/image/webp) を使用できます

## インストール

```sh
go get github.com/mayahiro/go-webp
```

## 使い方

次の例はJPEG画像をquality 80のlossy WebPへ変換します

```go
package main

import (
	"image/jpeg"
	"os"

	webp "github.com/mayahiro/go-webp"
)

func main() {
	src, err := os.Open("input.jpg")
	if err != nil {
		panic(err)
	}
	defer src.Close()

	img, err := jpeg.Decode(src)
	if err != nil {
		panic(err)
	}

	dst, err := os.Create("output.webp")
	if err != nil {
		panic(err)
	}
	defer dst.Close()

	err = webp.Encode(dst, img, &webp.Options{
		Compression: webp.CompressionLossy,
		Quality:     80,
	})
	if err != nil {
		panic(err)
	}
}
```

lossless WebPを書き出す場合はoptionsへnilを渡します

```go
err := webp.Encode(dst, img, nil)
```

リクエストのcontextを渡すと、エンコードをキャンセルできます

```go
err := webp.EncodeContext(ctx, dst, img, nil)
```

キャンセル時は `ctx.Err()` を返し、出力が不完全な状態で残る場合があります
詳細は [キャンセルの仕様](docs/encoder_ja.md#キャンセル) を参照してください

metadataを付与する場合は、画像とは別に取得したchunk payloadを渡します

```go
err := webp.EncodeWithMetadata(dst, img, nil, &webp.Metadata{
	ICCProfile: iccBytes,
	EXIF:       exifBytes,
	XMP:        xmpBytes,
})
```

payloadはそのまま格納し、`image.Image` からのmetadata抽出や、color profile、orientationの適用は行いません
metadataがnilまたは空の場合は、`Encode` と同じbytesを出力します
payloadの要件とキャンセル対応APIは [Metadata](docs/encoder_ja.md#metadata) を参照してください

## 性能

Apple M1 Max、darwin/arm64、Go 1.26.5での代表値です

| Encoding | Fixture | Time | Encoded size |
| --- | --- | ---: | ---: |
| Lossy Q75 | UI 256x256 | 11.921 ms | 2,906 bytes |
| Lossy Q75 | Photo-like 512x512 | 108.031 ms | 142,078 bytes |
| Lossless | Gradient 128x128 | 88.615 ms | 58 bytes |
| Lossless | Photo-like 512x512 | 579.021 ms | 2,916 bytes |

これらはローカルの開発参考値であり、環境をまたぐ性能保証ではありません
fixtureは決定的に生成したsynthetic imageです
計測方法と全結果は [Benchmarks](BENCHMARKS.md) を参照してください

## ドキュメント

- [Encoder guide](docs/encoder_ja.md): API、compression family、profile、alpha、制約、resource特性
- [Architecture](ARCHITECTURE.md): package境界とencoding pipeline
- [Benchmarks](BENCHMARKS.md): 再現可能な性能計測
- [Development guide](docs/development_ja.md): 検証とローカル比較のコマンド

## ライセンス

MIT
