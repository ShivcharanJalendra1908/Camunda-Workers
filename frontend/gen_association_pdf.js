const puppeteer = require('puppeteer');
const fs = require('fs');
const path = require('path');

(async () => {
  try {
    const mdPath = path.join(__dirname, '../docs/Plans/Association/association_integration_guide.md');
    const pdfPath = path.join(__dirname, '../docs/Plans/Association/association_integration_guide.pdf');
    
    if (!fs.existsSync(mdPath)) {
      console.error('Source Markdown file not found at:', mdPath);
      process.exit(1);
    }

    const mdContent = fs.readFileSync(mdPath, 'utf8');
    
    const htmlTemplate = `
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/github-markdown-css/5.5.1/github-markdown.min.css">
  <script src="https://cdn.jsdelivr.net/npm/marked/marked.min.js"></script>
  <script src="https://cdn.jsdelivr.net/npm/mermaid@10.9.1/dist/mermaid.min.js"></script>
  <style>
    body {
      box-sizing: border-box;
      min-width: 200px;
      margin: 0;
      padding: 0;
      background-color: white;
    }
    .markdown-body {
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
      font-size: 13px; /* Slightly smaller base font for neat print layout */
      line-height: 1.5;
    }
    /* Standardize tables for PDF print */
    .markdown-body table {
      display: table !important;
      width: 100% !important;
      table-layout: auto !important;
      border-collapse: collapse !important;
      margin: 15px 0 !important;
      page-break-inside: avoid !important;
    }
    .markdown-body th, .markdown-body td {
      border: 1px solid #dfe2e5 !important;
      padding: 8px 12px !important;
      word-wrap: break-word !important;
      word-break: break-word !important;
      white-space: normal !important;
    }
    .mermaid {
      background: white;
      display: flex;
      justify-content: center;
      margin: 20px 0;
      width: 100%;
      page-break-inside: avoid !important;
      break-inside: avoid !important;
    }
    .mermaid svg {
      max-width: 100% !important;
      height: auto !important;
      max-height: 380px !important; /* Cap vertical scaling of diagrams to prevent clipping / huge boxes */
    }
    /* Page break rules for clean rendering */
    h1, h2, h3, h4, h5, h6 {
      page-break-after: avoid !important;
      break-after: avoid !important;
    }
    pre, blockquote, img {
      page-break-inside: avoid !important;
      break-inside: avoid !important;
    }
    tr {
      page-break-inside: avoid !important;
      break-inside: avoid !important;
    }
  </style>
</head>
<body class="markdown-body">
  <div id="content"></div>
  <script>
    const markdown = ${JSON.stringify(mdContent)};
    
    // Parse Markdown
    document.getElementById('content').innerHTML = marked.parse(markdown);
 
    // Transform code blocks with class language-mermaid to div class="mermaid"
    const codes = document.querySelectorAll('pre code.language-mermaid');
    codes.forEach(code => {
      const pre = code.parentNode;
      const div = document.createElement('div');
      div.className = 'mermaid';
      div.textContent = code.textContent;
      pre.parentNode.replaceChild(div, pre);
    });
 
    // Initialize mermaid
    mermaid.initialize({ startOnLoad: false, theme: 'default' });
    
    // Render diagrams
    window.renderFinished = false;
    mermaid.run().then(() => {
      window.renderFinished = true;
    }).catch(err => {
      console.error('MERMAID RENDER ERROR:', err.message || err.str || err);
      window.renderFinished = true;
    });
  </script>
</body>
</html>
    `;
    
    console.log('Launching Puppeteer...');
    const browser = await puppeteer.launch({
      headless: true,
      args: ['--no-sandbox', '--disable-setuid-sandbox'],
      userDataDir: path.join(__dirname, '../tmp_puppeteer')
    });
    
    const page = await browser.newPage();
    page.on('console', msg => console.log('PAGE LOG:', msg.text()));
    
    // Set a wide viewport to prevent tables/elements from wrapping excessively during layout
    await page.setViewport({ width: 1200, height: 900 });
    
    page.setDefaultNavigationTimeout(0);
    await page.setContent(htmlTemplate, { waitUntil: 'domcontentloaded', timeout: 0 });
    
    console.log('Waiting for Mermaid diagrams to render...');
    // Wait for mermaid rendering to complete
    await page.waitForFunction('window.renderFinished === true', { timeout: 0 });
    
    // Wait an extra second to make sure styles render
    await new Promise(resolve => setTimeout(resolve, 2000));
    
    console.log('Generating PDF...');
    await page.pdf({
      path: pdfPath,
      format: 'A4',
      margin: {
        top: '20mm',
        right: '15mm',
        bottom: '20mm',
        left: '15mm'
      },
      printBackground: true,
      displayHeaderFooter: true,
      headerTemplate: '<div style="font-size: 8px; font-family: -apple-system, sans-serif; width: 100%; text-align: right; padding-right: 25px; color: #888;">LeMiCi Association Module - Integration Guide & Specification</div>',
      footerTemplate: '<div style="font-size: 8px; font-family: -apple-system, sans-serif; width: 100%; text-align: center; color: #888;">Page <span class="pageNumber"></span> of <span class="totalPages"></span></div>'
    });
    
    await browser.close();
    console.log('PDF generated successfully at: ' + pdfPath);
  } catch (error) {
    console.error('Error generating PDF:', error);
    process.exit(1);
  }
})();
