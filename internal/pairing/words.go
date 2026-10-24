package pairing

import "strings"

// wordlist is the alphabet for join codes: 256 short, common, distinct
// English words, so each word carries 8 bits and a 6-word code 48. Written for
// this project (no third-party list to license), and chosen to be easy to read
// aloud and type on a phone.
var wordlist = strings.Fields(`
	acorn actor adobe agent album alley amber anchor angle apple apron arrow
	aspen atlas attic autumn award bacon badge bagel baker bamboo banjo
	barley basil basket beach beacon beetle berry bison blanket blossom
	bonnet border bottle branch bread breeze brick bridge bronze brook
	bucket buffalo button cabin cactus camel camera canal candle canoe
	canyon carbon carpet carrot castle cedar cello chalk cherry chess
	chimney cider cinema circle clover cobalt cocoa comet copper coral
	cotton cradle crane crayon cricket crystal cupboard curtain daisy dancer
	delta desert diamond dolphin domino donkey dragon eagle easel echo elbow
	ember engine falcon feather fennel ferry fiddle fig flute forest fossil
	fountain fox galaxy garden garlic ginger glacier globe goose granite
	grape gravel guitar hammer harbor harvest hazel hedge helmet heron
	hollow honey horizon iceberg igloo indigo island ivory jacket jaguar
	jasmine jelly jigsaw juniper kayak kettle kitten koala ladder lagoon
	lantern lemon lily linen lizard lobster locket lotus magnet mango maple
	marble meadow melon mirror mitten moose mosaic muffin napkin nectar
	needle nickel noodle nutmeg oasis ocean olive onion orbit orchid otter
	oyster paddle palace panda paper parrot peach pebble pelican pepper
	piano pickle pigeon pillow pine planet plum pocket pony poppy potato
	pretzel puzzle quartz quilt rabbit radish raven ribbon river robin
	rocket saddle salmon sandal saturn scarf shell silver sketch sled
	sparrow spider spruce squash stable statue stone sunset swan tablet
	teapot thistle thunder tiger timber tomato topaz tractor trumpet tulip
	tunnel turtle umbrella valley velvet violin volcano waffle walnut walrus
	willow window winter wizard yarn yogurt zebra zipper
`)
