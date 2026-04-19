const { Markdown, toHtml } = require('/home/user/markdown/dist/markdown.js')
const { Jsonic } = require('jsonic')
const spec = require('/home/user/markdown/test/fixtures/commonmark-spec.json')
const md = Jsonic.make().use(Markdown, { html: true })
const arg = process.argv[2]
let shown = 0
for (const ex of spec) {
  if (arg && !(String(ex.example) === arg || ex.section.includes(arg))) continue
  let got = ''
  try { got = toHtml(md(ex.markdown)) } catch {}
  if (got !== ex.html) {
    shown++
    console.log(`--- ${ex.section} #${ex.example} ---`)
    console.log('INPUT :', JSON.stringify(ex.markdown))
    console.log('WANT  :', JSON.stringify(ex.html))
    console.log('GOT   :', JSON.stringify(got))
  }
}
if (!arg) {
  let pass = 0
  for (const ex of spec) {
    let got = ''
    try { got = toHtml(md(ex.markdown)) } catch {}
    if (got === ex.html) pass++
  }
  console.log('TOTAL:', pass, '/', spec.length)
}
