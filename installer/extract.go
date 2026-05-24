package installer

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"sd-studio-server/process"
)

func extractZip(zipPath, targetDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	tmpDir, err := os.MkdirTemp("", "sd-extract-*")
	if err != nil {
		return err
	}

	success := false
	defer func() {
		if !success {
			os.RemoveAll(tmpDir)
		}
	}()

	for _, f := range r.File {
		if f.Name == "" {
			continue
		}

		parts := strings.SplitN(f.Name, "/", 2)
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		relPath := parts[1]

		destPath := filepath.Join(tmpDir, relPath)

		if f.FileInfo().IsDir() {
			os.MkdirAll(destPath, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}

	if err := os.RemoveAll(targetDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove old target: %w", err)
	}

	if err := renameOrCopy(tmpDir, targetDir); err != nil {
		return fmt.Errorf("move to target: %w", err)
	}

	success = true
	return nil
}

func extractTgz(tgzPath, targetDir string) error {
	f, err := os.Open(tgzPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tmpDir, err := os.MkdirTemp("", "sd-extract-*")
	if err != nil {
		return err
	}

	success := false
	defer func() {
		if !success {
			os.RemoveAll(tmpDir)
		}
	}()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		parts := strings.SplitN(hdr.Name, "/", 2)
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		relPath := parts[1]

		destPath := filepath.Join(tmpDir, relPath)

		if hdr.Typeflag == tar.TypeDir {
			os.MkdirAll(destPath, os.FileMode(hdr.Mode))
			continue
		}

		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY, os.FileMode(hdr.Mode))
		if err != nil {
			return err
		}

		_, err = io.Copy(outFile, tr)
		outFile.Close()
		if err != nil {
			return err
		}
	}

	if err := os.RemoveAll(targetDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove old target: %w", err)
	}

	if err := renameOrCopy(tmpDir, targetDir); err != nil {
		return fmt.Errorf("move to target: %w", err)
	}

	success = true
	return nil
}

func extractBinaryFromTgz(tgzPath, binaryName, targetPath string, lb *process.RingBuffer) error {
	f, err := os.Open(tgzPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if matchBinaryName(filepath.Base(hdr.Name), binaryName) && hdr.Typeflag == tar.TypeReg {
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			out.Close()
			if err != nil {
				return err
			}
			lb.Write(fmt.Sprintf("Extracted %s from %s", binaryName, hdr.Name))
			return nil
		}
	}
	return fmt.Errorf("%s not found in archive", binaryName)
}

func extractBinaryFromTarZst(tarZstPath, binaryName, targetPath string, lb *process.RingBuffer) error {
	tmpDir, err := os.MkdirTemp("", "sd-extract-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command("tar", "-I", "zstd", "-xf", tarZstPath, "-C", tmpDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		cmd = exec.Command("tar", "--zstd", "-xf", tarZstPath, "-C", tmpDir)
		if output2, err2 := cmd.CombinedOutput(); err2 != nil {
			return fmt.Errorf("tar extract (tried -I zstd and --zstd): %s; %s: %w", strings.TrimSpace(string(output)), strings.TrimSpace(string(output2)), err2)
		}
	}

	var found string
	filepath.Walk(tmpDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return nil
		}
		if matchBinaryName(filepath.Base(path), binaryName) {
			found = path
		}
		return nil
	})
	if found == "" {
		return fmt.Errorf("%s not found in extracted archive", binaryName)
	}

	if err := copyFile(found, targetPath); err != nil {
		return err
	}
	lb.Write(fmt.Sprintf("Extracted %s from archive", binaryName))
	return nil
}

func extractBinaryFromZip(zipPath, binaryName, targetPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if matchBinaryName(filepath.Base(f.Name), binaryName) && !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				rc.Close()
				return err
			}
			_, err = io.Copy(out, rc)
			out.Close()
			rc.Close()
			return err
		}
	}
	return fmt.Errorf("%s not found in archive", binaryName)
}

func renameOrCopy(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := renameOrCopy(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return os.RemoveAll(src)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}

	_, err = io.Copy(out, in)
	out.Close()
	return err
}

func matchBinaryName(name, target string) bool {
	if name == target {
		return true
	}
	if runtime.GOOS == "windows" && name == target+".exe" {
		return true
	}
	return false
}

func archiveExt(url string) string {
	switch {
	case strings.HasSuffix(url, ".tar.zst"):
		return ".tar.zst"
	case strings.HasSuffix(url, ".tar.gz"):
		return ".tar.gz"
	default:
		return filepath.Ext(url)
	}
}
