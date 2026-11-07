# Icon source

`assets/icon.png` is a 512×512 crop of `assets/squawk.jpg` (1400×933), centred on
the bird's head:

```sh
convert assets/squawk.jpg -crop 760x760+320+0 +repage -resize 512x512 assets/icon.png
cp assets/icon.png internal/web/static/icon.png
cp assets/squawk.jpg internal/web/static/bird.jpg
```

The hero image on the Compose page uses `bird.jpg`; the header, favicon and
`assets/icon.png` use the square crop.
