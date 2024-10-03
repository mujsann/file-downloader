# File Downloader

A CLI Go application that downloads files from a specified URL by chunking the file into multiple parts and downloading them in parallel. This application utilizes goroutines to enhance download speed and efficiency.

## Features

- Downloads files in parallel parts (4 parts by default. You can customize the number of parts).
- Automatically concatenates the downloaded parts into a single file.
- Supports retrying failed downloads up to 3 times (can also be customized).

## Requirements

- Go 1.16 or higher

## Installation

1. Clone the repository:

   ```bash
   git clone git@github.com:mujsann/file-downloader.git
   cd file-downloader
   ```

2. Build the application:

   ```bash
   go build -o filedownloader main.go
   ```

## Usage

Run the application with the following command:

   ```bash
    ./filedownloader -url <file-download-url> -dest <local-destination-path>
   ```
