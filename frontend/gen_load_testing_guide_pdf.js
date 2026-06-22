const puppeteer = require('puppeteer');
const fs = require('fs');
const path = require('path');

(async () => {
  try {
    const mdPath = path.join(__dirname, '../docs/load_testing_architecture_guide.md');
    const pdfPath = path.join(__dirname, '../docs/load_testing_architecture_guide.pdf');
    
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
      max-width: 1000px;
      margin: 0 auto;
      padding: 40px;
      background-color: white;
    }
    .markdown-body {
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
      font-size: 14px;
    }
    .mermaid {
      background: white;
      display: flex;
      justify-content: center;
      margin: 30px 0;
      width: 100%;
    }
    .mermaid svg {
      width: 100% !important;
      height: auto !important;
      max-width: 100% !important;
    }
    /* Page break rules for printing */
    h1, h2, h3 {
      page-break-after: avoid;
    }
    pre, blockquote, table, img, .mermaid {
      page-break-inside: avoid;
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
      console.error(err);
      window.renderFinished = true;
    });
  </script>
</body>
</html>
    `;
    
    console.log('Launching Puppeteer...');
    const browser = await puppeteer.launch({
      headless: true,
      args: ['--no-sandbox', '--disable-setuid-sandbox']
    });
    
    const page = await browser.newPage();
    page.setDefaultNavigationTimeout(0);
    await page.setContent(htmlTemplate, { waitUntil: 'domcontentloaded', timeout: 0 });
    
    console.log('Waiting for Mermaid diagrams to render...');
    // Wait for mermaid rendering to complete
    await page.waitForFunction('window.renderFinished === true', { timeout: 0 });
    
    // Wait an extra second to make sure styles render
    await new Promise(resolve => setTimeout(resolve, 1500));
    
    console.log('Generating PDF...');
    await page.pdf({
      path: pdfPath,
      format: 'A4',
      margin: {
        top: '15mm',
        right: '15mm',
        bottom: '15mm',
        left: '15mm'
      },
      printBackground: true
    });
    
    await browser.close();
    console.log('PDF generated successfully at: ' + pdfPath);
  } catch (error) {
    console.error('Error generating PDF:', error);
    process.exit(1);
  }
})();
