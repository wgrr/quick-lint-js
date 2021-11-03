// Copyright (C) 2020  Matthew "strager" Glazar
// See end of file for extended copyright information.

package main

import "archive/tar"
import "archive/zip"
import "bytes"
import "compress/gzip"
import "crypto/sha1"
import "encoding/hex"
import "errors"
import "flag"
import "fmt"
import "io"
import "io/fs"
import "io/ioutil"
import "log"
import "os"
import "os/exec"
import "path/filepath"
import "strings"
import _ "embed"

//go:embed certificates/quick-lint-js.cer
var AppleCodesignCertificate []byte

type SigningStuff struct {
	AppleCodesignIdentity string // Common Name from the macOS Keychain.
	Certificate           []byte
	CertificateSHA1Hash   [20]byte
	PrivateKeyPKCS12Path  string
}

var TempDirs []string

func main() {
	defer RemoveTempDirs()

	var signingStuff SigningStuff
	signingStuff.Certificate = AppleCodesignCertificate

	flag.StringVar(&signingStuff.AppleCodesignIdentity, "AppleCodesignIdentity", "", "")
	flag.StringVar(&signingStuff.PrivateKeyPKCS12Path, "PrivateKeyPKCS12", "", "")
	flag.Parse()
	if flag.NArg() != 2 {
		os.Stderr.WriteString(fmt.Sprintf("error: source and destination directories\n"))
		os.Exit(2)
	}

	signingStuff.CertificateSHA1Hash = sha1.Sum(AppleCodesignCertificate)

	sourceDir := flag.Args()[0]
	destinationDir := flag.Args()[1]

	err := filepath.Walk(sourceDir, func(sourcePath string, sourceInfo fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(sourceDir, sourcePath)
		if err != nil {
			log.Fatal(err)
		}

		destinationPath := filepath.Join(destinationDir, relativePath)
		if sourceInfo.IsDir() {
			err = os.Mkdir(destinationPath, sourceInfo.Mode())
			if err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
		} else {
			err = CopyFileOrTransformArchive(relativePath, sourcePath, destinationPath, sourceInfo, signingStuff)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
}

func RemoveTempDirs() {
	for _, tempDir := range TempDirs {
		os.RemoveAll(tempDir)
	}
}

type FileTransformType int

const (
	NoTransform FileTransformType = iota
	AppleCodesign
	MicrosoftOsslsigncode
)

var filesToTransform map[string]map[string]FileTransformType = map[string]map[string]FileTransformType{
	"manual/macos-aarch64.tar.gz": map[string]FileTransformType{
		"quick-lint-js/bin/quick-lint-js": AppleCodesign,
	},
	"manual/macos.tar.gz": map[string]FileTransformType{
		"quick-lint-js/bin/quick-lint-js": AppleCodesign,
	},
	"manual/windows-arm.zip": map[string]FileTransformType{
		"bin/quick-lint-js.exe": MicrosoftOsslsigncode,
	},
	"manual/windows-arm64.zip": map[string]FileTransformType{
		"bin/quick-lint-js.exe": MicrosoftOsslsigncode,
	},
	"manual/windows.zip": map[string]FileTransformType{
		"bin/quick-lint-js.exe": MicrosoftOsslsigncode,
	},
	"npm/quick-lint-js-0.5.0.tgz": map[string]FileTransformType{
		"package/darwin-aarch64/bin/quick-lint-js": AppleCodesign,
		"package/darwin-x64/bin/quick-lint-js":     AppleCodesign,
		"package/win32-arm64/bin/quick-lint-js":    MicrosoftOsslsigncode,
		"package/win32-x64/bin/quick-lint-js":      MicrosoftOsslsigncode,
	},
	"vscode/quick-lint-js-0.5.0.vsix": map[string]FileTransformType{
		"extension/dist/quick-lint-js-vscode-node_darwin-arm64.node": AppleCodesign,
		"extension/dist/quick-lint-js-vscode-node_darwin-x64.node":   AppleCodesign,
		"extension/dist/quick-lint-js-vscode-node_win32-arm.node":    MicrosoftOsslsigncode,
		"extension/dist/quick-lint-js-vscode-node_win32-arm64.node":  MicrosoftOsslsigncode,
		"extension/dist/quick-lint-js-vscode-node_win32-ia32.node":   MicrosoftOsslsigncode,
		"extension/dist/quick-lint-js-vscode-node_win32-x64.node":    MicrosoftOsslsigncode,
	},
}

// If the file is an archive and has a file which needs to be signed, sign the
// embedded file and recreate the archive. Otherwise, copy the file verbatim.
func CopyFileOrTransformArchive(relativePath string, sourcePath string, destinationPath string, sourceInfo fs.FileInfo, signingStuff SigningStuff) error {
	if !sourceInfo.Mode().IsRegular() {
		return fmt.Errorf("expected regular file: %q", sourcePath)
	}

	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destinationFile, err := os.OpenFile(destinationPath,
		os.O_WRONLY|os.O_CREATE|os.O_TRUNC, sourceInfo.Mode().Perm())
	if err != nil {
		return err
	}
	fileComplete := false
	defer (func() {
		destinationFile.Close()
		if !fileComplete {
			os.Remove(destinationPath)
		}
	})()

	if strings.HasSuffix(relativePath, ".tar.gz") || strings.HasSuffix(relativePath, ".tgz") {
		archiveMembersToTransform := filesToTransform[relativePath]
		if archiveMembersToTransform != nil {
			if err := TransformTarGz(sourceFile, sourcePath, destinationFile, archiveMembersToTransform, signingStuff); err != nil {
				return err
			}
			fileComplete = true
			return nil
		}
	}

	if strings.HasSuffix(relativePath, ".vsix") || strings.HasSuffix(relativePath, ".zip") {
		archiveMembersToTransform := filesToTransform[relativePath]
		if archiveMembersToTransform != nil {
			if err := TransformZip(sourceFile, destinationFile, archiveMembersToTransform, signingStuff); err != nil {
				return err
			}
			fileComplete = true
			return nil
		}
	}

	// Default behavior: copy the file verbatim.
	_, err = io.Copy(destinationFile, sourceFile)
	if err != nil {
		return err
	}
	err = os.Chtimes(destinationPath, sourceInfo.ModTime(), sourceInfo.ModTime())
	if err != nil {
		return err
	}
	fileComplete = true
	return nil
}

type FileTransformResult struct {
	// If newFile is nil, then the file remains untouched.
	// If newFile is not nil, its contents are used.
	//
	// If newFile is not nil, Close will delete the file as if by
	// os.Remove(newFile.Name())
	newFile *os.File
}

func (self *FileTransformResult) UpdateTarHeader(header *tar.Header) error {
	if self.newFile != nil {
		newFileStat, err := self.newFile.Stat()
		if err != nil {
			return err
		}
		if header.Format == tar.FormatGNU || header.Format == tar.FormatPAX {
			header.AccessTime = newFileStat.ModTime()
			header.ChangeTime = newFileStat.ModTime()
		}
		header.ModTime = newFileStat.ModTime()
		header.Size = newFileStat.Size()
	}
	return nil
}

func (self *FileTransformResult) UpdateZipHeader(header *zip.FileHeader) error {
	if self.newFile != nil {
		newFileStat, err := self.newFile.Stat()
		if err != nil {
			return err
		}
		header.Modified = newFileStat.ModTime()
		header.UncompressedSize = uint32(newFileStat.Size())
		header.UncompressedSize64 = uint64(newFileStat.Size())
	}
	return nil
}

func (self *FileTransformResult) Close() {
	if self.newFile != nil {
		self.newFile.Close()
		os.Remove(self.newFile.Name())
		self.newFile = nil
	}
}

func TransformTarGz(
	sourceFile io.Reader,
	sourceFilePath string,
	destinationFile io.Writer,
	membersToTransform map[string]FileTransformType,
	signingStuff SigningStuff,
) error {
	return TransformTarGzGeneric(sourceFile, destinationFile,
		func(path string, file io.Reader) (FileTransformResult, error) {
			switch membersToTransform[path] {
			case AppleCodesign:
				log.Printf("signing with Apple codesign: %s:%s\n", sourceFilePath, path)
				transform, err := AppleCodesignTransform(path, file, signingStuff)
				if err != nil {
					return FileTransformResult{}, err
				}
				return transform, nil

			case MicrosoftOsslsigncode:
				log.Printf("signing with osslsigncode: %s:%s\n", sourceFilePath, path)
				transform, err := MicrosoftOsslsigncodeTransform(file, signingStuff)
				if err != nil {
					return FileTransformResult{}, err
				}
				return transform, nil

			default: // NoTransform
				return NoOpTransform(), nil
			}
		})
}

func TransformTarGzGeneric(
	sourceFile io.Reader,
	destinationFile io.Writer,
	transform func(path string, file io.Reader) (FileTransformResult, error),
) error {
	sourceUngzippedFile, err := gzip.NewReader(sourceFile)
	if err != nil {
		return err
	}
	destinationUngzippedFile := gzip.NewWriter(destinationFile)
	defer destinationUngzippedFile.Close()
	tarReader := tar.NewReader(sourceUngzippedFile)
	tarWriter := tar.NewWriter(destinationUngzippedFile)
	defer tarWriter.Close()
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}

		transformResult, err := transform(header.Name, tarReader)
		if err != nil {
			return err
		}
		defer transformResult.Close()

		if err := transformResult.UpdateTarHeader(header); err != nil {
			return err
		}
		var file io.Reader = tarReader
		if transformResult.newFile != nil {
			file = transformResult.newFile
		}
		if err := WriteTarEntry(header, file, tarWriter); err != nil {
			return err
		}
	}
	return nil
}

func TransformZip(
	sourceFile *os.File,
	destinationFile io.Writer,
	membersToTransform map[string]FileTransformType,
	signingStuff SigningStuff,
) error {
	return TransformZipGeneric(sourceFile, destinationFile,
		func(path string, file io.Reader) (FileTransformResult, error) {
			switch membersToTransform[path] {
			case AppleCodesign:
				log.Printf("signing with Apple codesign: %s:%s\n", sourceFile.Name(), path)
				transform, err := AppleCodesignTransform(path, file, signingStuff)
				if err != nil {
					return FileTransformResult{}, err
				}
				return transform, nil

			case MicrosoftOsslsigncode:
				log.Printf("signing with osslsigncode: %s:%s\n", sourceFile.Name(), path)
				transform, err := MicrosoftOsslsigncodeTransform(file, signingStuff)
				if err != nil {
					return FileTransformResult{}, err
				}
				return transform, nil

			default: // NoTransform
				return NoOpTransform(), nil
			}
		})
}

func TransformZipGeneric(
	sourceFile *os.File,
	destinationFile io.Writer,
	transform func(path string, file io.Reader) (FileTransformResult, error),
) error {
	sourceFileStat, err := sourceFile.Stat()
	if err != nil {
		return err
	}
	sourceZipFile, err := zip.NewReader(sourceFile, sourceFileStat.Size())
	if err != nil {
		return err
	}

	destinationZipFile := zip.NewWriter(destinationFile)
	defer destinationZipFile.Close()

	for _, zipEntry := range sourceZipFile.File {
		zipEntryFile, err := zipEntry.Open()
		if err != nil {
			return err
		}
		defer zipEntryFile.Close()

		transformResult, err := transform(zipEntry.Name, zipEntryFile)
		if err != nil {
			return err
		}
		defer transformResult.Close()

		if err := transformResult.UpdateZipHeader(&zipEntry.FileHeader); err != nil {
			return err
		}
		if transformResult.newFile == nil {
			rawZIPEntryFile, err := zipEntry.OpenRaw()
			if err != nil {
				return err
			}

			destinationZipEntryFile, err := destinationZipFile.CreateRaw(&zipEntry.FileHeader)
			if err != nil {
				return err
			}
			_, err = io.Copy(destinationZipEntryFile, rawZIPEntryFile)
			if err != nil {
				return err
			}
		} else {
			destinationZipEntryFile, err := destinationZipFile.CreateHeader(&zipEntry.FileHeader)
			if err != nil {
				return err
			}
			_, err = io.Copy(destinationZipEntryFile, transformResult.newFile)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func NoOpTransform() FileTransformResult {
	return FileTransformResult{
		newFile: nil,
	}
}

// originalPath need not be a path to a real file.
func AppleCodesignTransform(originalPath string, exe io.Reader, signingStuff SigningStuff) (FileTransformResult, error) {
	tempDir, err := ioutil.TempDir("", "quick-lint-js-sign-release")
	if err != nil {
		return FileTransformResult{}, err
	}
	TempDirs = append(TempDirs, tempDir)

	// Name the file the same as the original. The codesign utility
	// sometimes uses the file name as the Identifier, and we don't want the
	// Identifier being some random string.
	tempFile, err := os.Create(filepath.Join(tempDir, filepath.Base(originalPath)))
	if err != nil {
		return FileTransformResult{}, err
	}
	_, err = io.Copy(tempFile, exe)
	tempFile.Close()
	if err != nil {
		return FileTransformResult{}, err
	}

	if err := AppleCodesignFile(tempFile.Name(), signingStuff); err != nil {
		return FileTransformResult{}, err
	}
	if err := AppleCodesignVerifyFile(tempFile.Name(), signingStuff); err != nil {
		return FileTransformResult{}, err
	}

	// AppleCodesignFile replaced the file, so we can't just rewind
	// tempFile. Open the file again.
	signedFile, err := os.Open(tempFile.Name())
	if err != nil {
		os.Remove(tempFile.Name())
		return FileTransformResult{}, err
	}

	return FileTransformResult{
		newFile: signedFile,
	}, nil
}

func AppleCodesignFile(filePath string, signingStuff SigningStuff) error {
	signCommand := []string{"codesign", "--sign", signingStuff.AppleCodesignIdentity, "--force", "--", filePath}
	process := exec.Command(signCommand[0], signCommand[1:]...)
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	if err := process.Start(); err != nil {
		return err
	}
	if err := process.Wait(); err != nil {
		return err
	}
	return nil
}

func AppleCodesignVerifyFile(filePath string, signingStuff SigningStuff) error {
	requirements := fmt.Sprintf("certificate leaf = H\"%s\"", hex.EncodeToString(signingStuff.CertificateSHA1Hash[:]))

	signCommand := []string{"codesign", "-vvv", fmt.Sprintf("-R=%s", requirements), "--", filePath}
	process := exec.Command(signCommand[0], signCommand[1:]...)
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	if err := process.Start(); err != nil {
		return err
	}
	if err := process.Wait(); err != nil {
		return err
	}

	return nil
}

func MicrosoftOsslsigncodeTransform(exe io.Reader, signingStuff SigningStuff) (FileTransformResult, error) {
	tempDir, err := ioutil.TempDir("", "quick-lint-js-sign-release")
	if err != nil {
		return FileTransformResult{}, err
	}
	TempDirs = append(TempDirs, tempDir)

	unsignedFile, err := os.Create(filepath.Join(tempDir, "unsigned.exe"))
	if err != nil {
		return FileTransformResult{}, err
	}
	defer os.Remove(unsignedFile.Name())
	_, err = io.Copy(unsignedFile, exe)
	unsignedFile.Close()
	if err != nil {
		return FileTransformResult{}, err
	}

	signedFilePath := filepath.Join(tempDir, "signed.exe")
	if err := MicrosoftOsslsigncodeFile(unsignedFile.Name(), signedFilePath, signingStuff); err != nil {
		return FileTransformResult{}, err
	}
	if err := MicrosoftOsslsigncodeVerifyFile(signedFilePath, signingStuff); err != nil {
		return FileTransformResult{}, err
	}

	signedFile, err := os.Open(signedFilePath)
	if err != nil {
		os.Remove(signedFilePath)
		return FileTransformResult{}, err
	}

	return FileTransformResult{
		newFile: signedFile,
	}, nil
}

func MicrosoftOsslsigncodeFile(inFilePath string, outFilePath string, signingStuff SigningStuff) error {
	signCommand := []string{
		"osslsigncode", "sign",
		"-pkcs12", signingStuff.PrivateKeyPKCS12Path,
		"-t", "http://timestamp.digicert.com",
		"-in", inFilePath,
		"-out", outFilePath,
	}
	process := exec.Command(signCommand[0], signCommand[1:]...)
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	if err := process.Start(); err != nil {
		return err
	}
	if err := process.Wait(); err != nil {
		return err
	}
	return nil
}

func MicrosoftOsslsigncodeVerifyFile(filePath string, signingStuff SigningStuff) error {
	certificatePEMFile, err := ioutil.TempFile("", "quick-lint-js-sign-release")
	if err != nil {
		return err
	}
	certificatePEMFile.Close()
	defer os.Remove(certificatePEMFile.Name())

	process := exec.Command(
		"openssl", "x509",
		"-inform", "der",
		"-outform", "pem",
		"-out", certificatePEMFile.Name(),
	)
	certificateReader := bytes.NewReader(signingStuff.Certificate)
	process.Stdin = certificateReader
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	if err := process.Start(); err != nil {
		return err
	}
	if err := process.Wait(); err != nil {
		return err
	}

	process = exec.Command(
		"osslsigncode", "verify",
		"-in", filePath,
		"-CAfile", certificatePEMFile.Name(),
	)
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	if err := process.Start(); err != nil {
		return err
	}
	if err := process.Wait(); err != nil {
		return err
	}

	return nil
}

func WriteTarEntry(header *tar.Header, file io.Reader, output *tar.Writer) error {
	if err := output.WriteHeader(header); err != nil {
		return err
	}
	bytesWritten, err := io.Copy(output, file)
	if err != nil {
		return err
	}
	if bytesWritten != header.Size {
		return fmt.Errorf("failed to write entire file")
	}
	return nil
}

// quick-lint-js finds bugs in JavaScript programs.
// Copyright (C) 2020  Matthew "strager" Glazar
//
// This file is part of quick-lint-js.
//
// quick-lint-js is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// quick-lint-js is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with quick-lint-js.  If not, see <https://www.gnu.org/licenses/>.
