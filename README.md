# HumanTXT API

## What?
Get the Human Readable Text of a website with additional informations via API call

## How?
https://humantxt.acona.app/?url=https://orf.at

### Change format output
by default the output of the content is in text format but can be changed to markdown or html  
https://humantxt.acona.app/?url=https://orf.at&format=markdown  
https://humantxt.acona.app/?url=https://orf.at&format=html  

### Submit HTML via POST
Send the page as the request body. `url` stays required: it is the base for relative links and picks the site extractor.

```console
$ curl -X POST --data-binary @page.html 'https://humantxt.acona.app/?url=https://orf.at&format=markdown'
```
