---
name: pdf-tools
description: Extract text and tables from PDF files and convert them to Markdown.
---

# PDF tools

SPIKE-MARKER-PDF-TOOLS

1. Run `pdftotext -layout input.pdf -` to get the text.
2. Rebuild tables by splitting columns on runs of two or more spaces.
3. Write the result as Markdown, one heading per page.
