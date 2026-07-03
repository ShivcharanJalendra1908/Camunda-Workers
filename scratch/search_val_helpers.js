const fs = require('fs');
const content = fs.readFileSync('internal/workers/infrastructure/build-response/handler.go', 'utf8');
const lines = content.split('\n');
lines.forEach((line, idx) => {
    if (line.includes('getMapVal') || line.includes('getArrayVal')) {
        console.log(`Line ${idx + 1}: ${line.trim()}`);
    }
});
