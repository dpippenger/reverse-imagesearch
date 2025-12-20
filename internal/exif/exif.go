package exif

import (
	"fmt"
	"image"
	"os"
	"strings"

	"github.com/rwcarlsen/goexif/exif"
)

// Data holds extracted EXIF metadata
type Data struct {
	Make         string `json:"make,omitempty"`
	Model        string `json:"model,omitempty"`
	DateTime     string `json:"dateTime,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	FileSize     int64  `json:"fileSize,omitempty"`
	Orientation  string `json:"orientation,omitempty"`
	FNumber      string `json:"fNumber,omitempty"`
	ExposureTime string `json:"exposureTime,omitempty"`
	ISO          string `json:"iso,omitempty"`
	FocalLength  string `json:"focalLength,omitempty"`
	LensModel    string `json:"lensModel,omitempty"`
	Software     string `json:"software,omitempty"`
	GPSLatitude  string `json:"gpsLatitude,omitempty"`
	GPSLongitude string `json:"gpsLongitude,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Extract reads EXIF metadata from an image file
func Extract(path string) Data {
	data := Data{}

	file, err := os.Open(path)
	if err != nil {
		data.Error = "Cannot open file"
		return data
	}
	defer file.Close()

	// Get file size
	if fileInfo, err := file.Stat(); err == nil {
		data.FileSize = fileInfo.Size()
	}

	// Get image dimensions
	img, _, err := image.DecodeConfig(file)
	if err == nil {
		data.Width = img.Width
		data.Height = img.Height
	}

	// Reset file position for EXIF reading
	file.Seek(0, 0)

	x, err := exif.Decode(file)
	if err != nil {
		data.Error = "No EXIF data"
		return data
	}

	// Helper to safely get string tag
	getString := func(tag exif.FieldName) string {
		t, err := x.Get(tag)
		if err != nil {
			return ""
		}
		s, err := t.StringVal()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(s)
	}

	data.Make = getString(exif.Make)
	data.Model = getString(exif.Model)
	data.Software = getString(exif.Software)

	// DateTime
	if dt, err := x.DateTime(); err == nil {
		data.DateTime = dt.Format("2006-01-02 15:04:05")
	}

	// Orientation
	if orient, err := x.Get(exif.Orientation); err == nil {
		if v, err := orient.Int(0); err == nil {
			orientations := map[int]string{
				1: "Normal",
				2: "Flipped horizontal",
				3: "Rotated 180°",
				4: "Flipped vertical",
				5: "Rotated 90° CCW, flipped",
				6: "Rotated 90° CW",
				7: "Rotated 90° CW, flipped",
				8: "Rotated 90° CCW",
			}
			if name, ok := orientations[v]; ok {
				data.Orientation = name
			}
		}
	}

	// FNumber (aperture)
	if fn, err := x.Get(exif.FNumber); err == nil {
		if num, denom, err := fn.Rat2(0); err == nil && denom != 0 {
			data.FNumber = fmt.Sprintf("f/%.1f", float64(num)/float64(denom))
		}
	}

	// Exposure time
	if et, err := x.Get(exif.ExposureTime); err == nil {
		if num, denom, err := et.Rat2(0); err == nil && denom != 0 {
			if num < denom {
				data.ExposureTime = fmt.Sprintf("1/%d s", denom/num)
			} else {
				data.ExposureTime = fmt.Sprintf("%.1f s", float64(num)/float64(denom))
			}
		}
	}

	// ISO
	if iso, err := x.Get(exif.ISOSpeedRatings); err == nil {
		if v, err := iso.Int(0); err == nil {
			data.ISO = fmt.Sprintf("ISO %d", v)
		}
	}

	// Focal length
	if fl, err := x.Get(exif.FocalLength); err == nil {
		if num, denom, err := fl.Rat2(0); err == nil && denom != 0 {
			data.FocalLength = fmt.Sprintf("%.0f mm", float64(num)/float64(denom))
		}
	}

	// Lens model
	if lens, err := x.Get(exif.LensModel); err == nil {
		if s, err := lens.StringVal(); err == nil {
			data.LensModel = strings.TrimSpace(s)
		}
	}

	// GPS coordinates
	if lat, lon, err := x.LatLong(); err == nil {
		data.GPSLatitude = fmt.Sprintf("%.6f", lat)
		data.GPSLongitude = fmt.Sprintf("%.6f", lon)
	}

	return data
}
