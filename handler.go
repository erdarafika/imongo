package main

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"
)

type ImgHandler struct {
}

func (h *ImgHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	log.Println(req.URL.Path)
	switch req.Method {
	case "GET":
		h.handleGET(w, req)
	case "POST":
		h.handlePOST(w, req)
	}
}

func (h *ImgHandler) handleGET(w http.ResponseWriter, req *http.Request) {
	s := MgoSession.Copy()
	defer s.Close()

	reqName, path := h.convertPath(req.URL.Path)
	if reqName == "favicon.ico" {
		return
	}

	name, size := h.getNameAndSize(reqName)

	document, err := new(Document).Find(s, name, path)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if document.ContentType != "" {
		w.Header().Set("Content-Type", document.ContentType)
	}

	// create cache folder if necessary
	cacheFilePath, _ := filepath.Abs(filepath.Clean(Configuration.CacheFolder + req.URL.Path))
	dir := filepath.Dir(cacheFilePath)
	err = os.MkdirAll(dir, 0755)
	if err != nil {
		log.Panicln(err)
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "can not make cache folder")
		return
	}

	// create cache image and wrap a multiwriter
	cacheFile, _ := os.Create(cacheFilePath)
	defer cacheFile.Close()
	multiWriter := io.MultiWriter(w, cacheFile)

	if size != nil {
		origin, format, err := image.Decode(bytes.NewBuffer(document.Binary))
		if err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(err.Error()))
			return
		}

		var img image.Image
		switch size.Option {
		case 0:
			img = imaging.Resize(origin, size.Width, size.Height, imaging.CatmullRom)
		case 1:
			img = imaging.Thumbnail(origin, size.Width, size.Height, imaging.CatmullRom)
		}

		err = h.writeImage(multiWriter, img, format)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(err.Error()))
			return
		}
	} else {
		multiWriter.Write(document.Binary)
	}
}

func (h *ImgHandler) handlePOST(w http.ResponseWriter, req *http.Request) {
	s := MgoSession.Copy()
	defer s.Close()

	name, path := h.convertPath(req.URL.Path)
	document, _ := new(Document).Find(s, name, path)
	document.Name = name
	document.Path = path

	origin, format, err := image.Decode(req.Body)
	if err != nil {
		log.Panicln(err)
		io.WriteString(w, err.Error())
		return
	}

	// image is larger than the specified stored size, we need to resize it
	var img image.Image
	if (Configuration.StoredSize.Width > 0 && origin.Bounds().Dx() > Configuration.StoredSize.Width) ||
		(Configuration.StoredSize.Height > 0 && origin.Bounds().Dy() > Configuration.StoredSize.Height) {
		img = imaging.Resize(origin, Configuration.StoredSize.Width, Configuration.StoredSize.Height, imaging.CatmullRom)
	} else {
		img = origin
	}

	buf := new(bytes.Buffer)
	err = h.writeImage(buf, img, format)
	if err != nil {
		log.Panicln(err)
		io.WriteString(w, err.Error())
		return
	}
	document.Binary = buf.Bytes()

	contentType := req.Header.Get("Content-Type")
	if contentType != "" {
		document.ContentType = contentType
	}

	err = document.Save(s)
	if err != nil {
		log.Panicln(err)
		io.WriteString(w, err.Error())
	} else {
		io.WriteString(w, "stored\n")
	}
}

func (h *ImgHandler) convertPath(urlPath string) (string, string) {
	var path []string
	folders := strings.Split(urlPath, "/")
	for ind, folder := range folders {
		trimFolder := strings.Trim(folder, " ")
		if trimFolder != "" && ind != len(folders)-1 {
			path = append(path, strings.ToLower(trimFolder))
		}
	}

	name := strings.ToLower(folders[len(folders)-1])
	return name, strings.Join(path, ",")
}

func (h *ImgHandler) getNameAndSize(reqName string) (name string, size *Size) {
	delimiterPos := strings.LastIndex(reqName, "__")
	if delimiterPos > 0 {
		ext := strings.ToLower(filepath.Ext(reqName))
		basename := strings.TrimSuffix(reqName, ext)
		name = strings.ToLower(reqName[0:delimiterPos] + ext)
		reqSize := basename[delimiterPos+2:]

		switch {
		case strings.Contains(reqSize, "x"):
			reqWidth, _ := strconv.Atoi(strings.Split(reqSize, "x")[0])
			reqHeight, _ := strconv.Atoi(strings.Split(reqSize, "x")[1])

			size = &Size{Width: reqWidth, Height: reqHeight, Option: 0}
		case strings.Contains(reqSize, "z"):
			reqWidth, _ := strconv.Atoi(strings.Split(reqSize, "z")[0])
			reqHeight, _ := strconv.Atoi(strings.Split(reqSize, "z")[1])

			size = &Size{Width: reqWidth, Height: reqHeight, Option: 1}
		}
	} else {
		name = reqName
	}
	return
}

func (h *ImgHandler) writeImage(w io.Writer, img image.Image, format string) (err error) {
	switch format {
	case "jpg", "jpeg":
		var rgba *image.RGBA
		if nrgba, ok := img.(*image.NRGBA); ok {
			if nrgba.Opaque() {
				rgba = &image.RGBA{
					Pix:    nrgba.Pix,
					Stride: nrgba.Stride,
					Rect:   nrgba.Rect,
				}
			}
		}
		if rgba != nil {
			err = jpeg.Encode(w, rgba, &jpeg.Options{Quality: 95})
		} else {
			err = jpeg.Encode(w, img, &jpeg.Options{Quality: 95})
		}
	case "png":
		err = png.Encode(w, img)
	default:
		err = fmt.Errorf("unknown format when writting %v", format)
	}
	return
}
