# go-webp

[English](README.md)

go-webpはGo標準の `image.Image` インターフェイス向けに、画質と圧縮率を重視したWebP encoding libraryです
Go標準の画像encoderに近い小さなAPIで、静止画のVP8L lossless、VP8L near-lossless、VP8 lossyを書き出します

## 特徴

- cgo、native library、architecture固有assemblyを使わないpure Go実装
- lossless、near-lossless、true VP8 lossy encoding
- すべてのcompression familyでalphaを保持
- fast、balanced、best-compression、auto、low-memoryのcompression profile
- 主要な標準画像型の専用経路と、一般的な `image.Image` fallback
- Go 1.25.0以上

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

## 性能

Apple M1 Max、darwin/arm64、Go 1.26.5での代表値です

| Encoding | Fixture | Time | Encoded size |
| --- | --- | ---: | ---: |
| Lossy Q75 | UI 256x256 | 17.959 ms | 2,906 bytes |
| Lossy Q75 | Photo-like 512x512 | 117.857 ms | 142,078 bytes |
| Lossless | Gradient 128x128 | 50.188 ms | 58 bytes |
| Lossless | Photo-like 512x512 | 1,698.321 ms | 23,770 bytes |

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
