const { execSync } = require('child_process');
const fs = require('fs');

const oldContent = execSync('git show cd42fe1e9350a228e434552435c06bea0313bdd8~1:internal/workers/infrastructure/build-response/handler.go', { maxBuffer: 10 * 1024 * 1024 }).toString();
const lines = oldContent.split('\n');
let inside = false;
const out = [];
for (let i = 0; i < lines.length; i++) {
    if (lines[i].includes('func (h *Handler) buildAssociationDetailResponse')) {
        inside = true;
    }
    if (inside) {
        out.push(`${i+1}: ${lines[i]}`);
        if (lines[i].startsWith('}')) {
            // Check if it's the end of the function (since it closes at root)
            if (out.length > 50 && lines[i].trim() === '}') {
                inside = false;
            }
        }
    }
}
fs.writeFileSync('scratch/old_detail_response.go', out.join('\n'));
console.log('Done!');
