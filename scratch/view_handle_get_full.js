const fs = require('fs');
const content = fs.readFileSync('internal/workers/data-access/franchise-postgres/handler.go', 'utf8');
const lines = content.split('\n');
let inside = false;
const out = [];
for (let i = 0; i < lines.length; i++) {
    if (lines[i].includes('func (h *Handler) handleGetFullFranchise')) {
        inside = true;
    }
    if (inside) {
        out.push(`${i+1}: ${lines[i]}`);
        if (lines[i].startsWith('}')) {
            if (out.length > 50 && lines[i].trim() === '}') {
                inside = false;
            }
        }
    }
}
console.log(out.join('\n'));
