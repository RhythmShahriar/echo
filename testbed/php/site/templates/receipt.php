// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

Hello <?php echo $name; ?>,

Thanks for your wholesale order <?php echo $order; ?> for <?php echo $company; ?>.

<?php foreach ( $items as $item ) : ?>
  <?php echo $item[1]; ?> x <?php echo $item[0]; ?>  $<?php echo number_format( $item[2], 2 ); ?>

<?php endforeach; ?>

We'll invoice you when it ships.

Bean & Leaf Wholesale
https://shop.example.com/wholesale/
