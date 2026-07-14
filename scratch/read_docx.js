const fs = require('fs');
const { execSync } = require('child_process');

// Read docx as zip and extract word/document.xml
const AdmZip = require('adm-zip');

try {
  const zip = new AdmZip('docs/Plans/Association/Association Homepage Search Query New.docx');
  const entry = zip.getEntry('word/document.xml');
  const xml = entry.getData().toString('utf8');
  
  // Simple regex to extract text from <w:t> tags grouped by paragraphs
  const paragraphs = xml.split('</w:p>');
  for (const p of paragraphs) {
    const texts = (p.match(/<w:t[^>]*>([^<]*)<\/w:t>/g) || [])
      .map(t => t.replace(/<[^>]+>/g, ''));
    const line = texts.join('');
    if (line.trim()) console.log(line.trim());
  }
} catch(e) {
  // Fallback: use built-in zlib
  const zlib = require('zlib');
  const { Readable } = require('stream');
  
  // Manual zip reading
  const buffer = fs.readFileSync('docs/Plans/Association/Association Homepage Search Query New.docx');
  
  // Find word/document.xml in zip
  let offset = 0;
  while (offset < buffer.length - 4) {
    if (buffer.readUInt32LE(offset) === 0x04034b50) { // Local file header
      const fnLen = buffer.readUInt16LE(offset + 26);
      const extraLen = buffer.readUInt16LE(offset + 28);
      const compSize = buffer.readUInt32LE(offset + 18);
      const fname = buffer.toString('utf8', offset + 30, offset + 30 + fnLen);
      const dataStart = offset + 30 + fnLen + extraLen;
      
      if (fname === 'word/document.xml') {
        const method = buffer.readUInt16LE(offset + 8);
        let content;
        if (method === 8) { // Deflate
          content = zlib.inflateRawSync(buffer.slice(dataStart, dataStart + compSize)).toString('utf8');
        } else {
          content = buffer.toString('utf8', dataStart, dataStart + compSize);
        }
        
        const paragraphs = content.split('</w:p>');
        for (const p of paragraphs) {
          const texts = (p.match(/<w:t[^>]*>([^<]*)<\/w:t>/g) || [])
            .map(t => t.replace(/<[^>]+>/g, ''));
          const line = texts.join('');
          if (line.trim()) console.log(line.trim());
        }
        break;
      }
      offset = dataStart + compSize;
    } else {
      offset++;
    }
  }
}
