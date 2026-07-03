const fs = require('fs');

const file1 = 'internal/workers/data-access/query-postgresql/handler_test.go';
const file2 = 'internal/workers/data-access/franchise-postgres/handler_test.go';

[file1, file2].forEach(file => {
    if (fs.existsSync(file)) {
        console.log(`=== ${file} ===`);
        const content = fs.readFileSync(file, 'utf8');
        const lines = content.split('\n');
        lines.forEach((line, idx) => {
            if (line.includes('ExpectQuery') || line.includes('SELECT id, name')) {
                console.log(`Line ${idx + 1}: ${line.trim()}`);
            }
        });
    }
});
