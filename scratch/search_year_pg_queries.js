const fs = require('fs');
const path = require('path');

const dir = 'internal/workers/data-access/query-postgresql/queries';
fs.readdirSync(dir).forEach(file => {
    if (file.endsWith('.go')) {
        const content = fs.readFileSync(path.join(dir, file), 'utf8');
        const lines = content.split('\n');
        lines.forEach((line, idx) => {
            if (line.includes('year') || line.includes('founded')) {
                console.log(`${file}:${idx + 1}: ${line.trim()}`);
            }
        });
    }
});
